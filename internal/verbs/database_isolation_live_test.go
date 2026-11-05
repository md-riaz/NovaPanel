package verbs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

// Run only inside a disposable, network-none MariaDB 10.11 container. Never run
// against a host database: this test creates schemas and exercises real grants.
func TestMariaDBClaimedCredentialIsolation(t *testing.T) {
	if os.Getenv("NOVAPANEL_ISOLATION_TEST") != "1" {
		t.Skip("requires disposable MariaDB fixture")
	}
	ctx := context.Background()
	run := func(args []string, sql, password string) (string, int, error) {
		env := []string{}
		if password != "" {
			env = append(env, "MYSQL_PWD="+password)
		}
		out, stderr, code, err := (exec.Real{}).Run(ctx, "/usr/bin/mariadb", args, exec.RunOpts{Stdin: strings.NewReader(sql), Env: env})
		return string(out) + string(stderr), code, err
	}
	rootArgs := []string{"--no-defaults", "--protocol=socket", "--user=root", "--batch"}
	root := func(sql string) {
		t.Helper()
		out, code, err := run(rootArgs, sql, "")
		if err != nil || code != 0 {
			t.Fatalf("fixture root SQL failed (%d): %v: %s", code, err, out)
		}
	}
	root("SELECT 1;") // Explicitly enabled with no fixture must fail, not skip.
	suffix, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	suffix = strings.ToLower(suffix[12:])
	name := "iso_" + suffix
	other := "other_" + suffix
	wildcard := "isox" + suffix // matches an unescaped underscore in name
	root(fmt.Sprintf("CREATE DATABASE `%s`; CREATE DATABASE `%s`; CREATE TABLE `%s`.probe (n INT); CREATE TABLE `%s`.probe (n INT);", other, wildcard, other, wildcard))
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	log, err := audit.Open(s, filepath.Join(t.TempDir(), "audit"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	seal, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	a := agent.New()
	a.Store = s
	a.Audit = log
	a.Secret = seal
	a.Exec = exec.Real{}
	server := rpc.NewServer(a)
	server.ConfigureJobs()
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(workerCtx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	caller := rpc.Caller{Kind: "user", Role: "admin", Session: "owner", MFAat: time.Now().UnixMilli()}
	dispatch := func(verb string, args any) jobs.EnqueueResult {
		t.Helper()
		b, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		r := server.Dispatch(rpc.WithCaller(ctx, caller), rpc.Request{Verb: verb, Args: b})
		if !r.OK {
			t.Fatalf("%s: %+v", verb, r)
		}
		return r.Data.(jobs.EnqueueResult)
	}
	execute := func(r jobs.EnqueueResult) map[string]string {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		var j jobs.Job
		var err error
		for {
			j, err = s.GetJob(ctx, r.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if j.State == "done" || j.State == "failed" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("worker timeout")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if j.State != "done" {
			t.Fatalf("job failed: %+v", j)
		}
		var result map[string]string
		if err = json.Unmarshal(j.Result, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	create := dispatch("database.create", map[string]string{"name": name})
	created := execute(create)
	claimReply := server.Dispatch(rpc.WithCaller(ctx, caller), rpc.Request{Verb: "job.claim", Args: []byte(fmt.Sprintf(`{"job_id":%q,"claim":%q}`, create.Job.ID, create.ClaimToken))})
	if !claimReply.OK {
		t.Fatalf("claim RPC: %+v", claimReply)
	}
	claim := claimReply.Data.(map[string]any)
	password, ok := claim["password"].(string)
	if !ok || password == "" {
		t.Fatal("missing claimed password")
	}
	tcp := []string{"--no-defaults", "--protocol=tcp", "--host=127.0.0.1", "--user=" + created["db_user"], "--batch"}
	allowed := func(pass string) {
		t.Helper()
		out, code, err := run(tcp, "CREATE TABLE `"+name+"`.owned (n INT); INSERT INTO `"+name+"`.owned VALUES (1); SELECT * FROM `"+name+"`.owned; DROP TABLE `"+name+"`.owned;", pass)
		if err != nil || code != 0 || !strings.Contains(out, "1") {
			t.Fatalf("own schema unavailable (%d): %v: %s", code, err, out)
		}
	}
	denied := func(args []string, pass, sql string, codes ...string) {
		t.Helper()
		out, code, err := run(args, sql, pass)
		if code == 0 || err == nil {
			t.Fatalf("unexpected success: %s", sql)
		}
		for _, c := range codes {
			if strings.Contains(out, "ERROR "+c+" ") {
				return
			}
		}
		t.Fatalf("expected MariaDB denial, got (%d): %v: %s", code, err, out)
	}
	allowed(password)
	for _, db := range []string{other, wildcard, "novapanel"} {
		table := "probe"
		if db == "novapanel" {
			table = "managed_objects"
		}
		denied(tcp, password, "SELECT * FROM `"+db+"`.`"+table+"`;", "1044", "1142")
	}
	mismatch := []string{"--no-defaults", "--protocol=socket", "--user=" + created["db_user"], "--batch"}
	denied(mismatch, password, "SELECT 1;", "1045", "1698")
	rotation := dispatch("database.rotate_password", map[string]string{"db_id": created["db_id"]})
	execute(rotation)
	rotated, err := a.Jobs.Claim(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner"}), rotation.Job.ID, rotation.ClaimToken)
	if err != nil {
		t.Fatal(err)
	}
	next, ok := rotated["password"].(string)
	if !ok || next == "" || next == password {
		t.Fatal("invalid rotated claim")
	}
	allowed(next)
	denied(tcp, password, "SELECT 1;", "1045")
	if _, err := a.Jobs.Claim(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner"}), rotation.Job.ID, rotation.ClaimToken); err == nil {
		t.Fatal("rotation claim returned twice")
	}
	deleted := dispatch("database.delete", map[string]string{"db_id": created["db_id"]})
	execute(deleted)
	out, code, err := run(rootArgs, fmt.Sprintf("SELECT (SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s')+(SELECT COUNT(*) FROM mysql.user WHERE User='%s' AND Host='127.0.0.1')+(SELECT COUNT(*) FROM novapanel.managed_objects WHERE (kind='schema' AND name='%s') OR (kind='user' AND name='%s@127.0.0.1'));", name, created["db_user"], name, created["db_user"]), "")
	if err != nil || code != 0 || !strings.Contains(out, "\n0\n") {
		t.Fatalf("delete left objects or provenance (%d): %v: %s", code, err, out)
	}
}
