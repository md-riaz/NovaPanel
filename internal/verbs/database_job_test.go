package verbs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

type interruptAfterSchema struct {
	delegate exec.Runner
	stopped  bool
}

func (r *interruptAfterSchema) Run(ctx context.Context, program string, args []string, o exec.RunOpts) ([]byte, []byte, int, error) {
	b, err := io.ReadAll(o.Stdin)
	if err != nil {
		return nil, nil, -1, err
	}
	o.Stdin = strings.NewReader(string(b))
	if !r.stopped && strings.Contains(string(b), "INSERT INTO novapanel.managed_objects") && strings.Contains(string(b), "'user'") {
		r.stopped = true
		return nil, nil, 1, nil
	}
	return r.delegate.Run(ctx, program, args, o)
}

type databaseRunner struct {
	calls   int
	fail    bool
	objects map[string]string
}

var markerKey = regexp.MustCompile(`kind='(schema|user)' AND name='([^']+)'`)
var markerInsert = regexp.MustCompile(`VALUES\('(schema|user)','([^']+)','([^']+)','reserved',`)

func (r *databaseRunner) Run(_ context.Context, _ string, _ []string, o exec.RunOpts) ([]byte, []byte, int, error) {
	r.calls++
	b, _ := io.ReadAll(o.Stdin)
	if r.fail {
		return nil, nil, 1, nil
	}
	if r.objects == nil {
		r.objects = make(map[string]string)
	}
	s := string(b)
	if strings.Contains(s, "FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='novapanel'") {
		return []byte("1\n"), nil, 0, nil
	}
	if strings.HasPrefix(s, "SELECT CONCAT(") {
		return []byte("1:5:5:2:2:0:0:0\n"), nil, 0, nil
	}
	if strings.HasPrefix(s, "SELECT (\n") {
		return []byte("0\n"), nil, 0, nil
	}
	if m := markerInsert.FindStringSubmatch(s); m != nil {
		r.objects[m[1]+":"+m[2]] = "reserved\t0\t" + m[3]
	} else if m := markerKey.FindStringSubmatch(s); m != nil {
		key := m[1] + ":" + m[2]
		state, ok := r.objects[key]
		if strings.HasPrefix(s, "SELECT COALESCE") {
			if !ok {
				state = "none\t0\t"
			}
			if !strings.Contains(s, "SELECT COUNT(*)") {
				state = strings.Split(state, "\t")[0]
			}
			return []byte(state + "\n"), nil, 0, nil
		}
		if strings.Contains(s, "SET phase='created'") {
			r.objects[key] = "created\t1\t" + strings.Split(state, "\t")[2]
		} else if strings.Contains(s, "SET phase='deleting'") {
			r.objects[key] = "deleting\t1\t" + strings.Split(state, "\t")[2]
		} else if strings.HasPrefix(s, "DELETE FROM") {
			delete(r.objects, key)
		}
	} else if strings.HasPrefix(s, "SELECT COUNT(*)") {
		return []byte("0\n"), nil, 0, nil
	}
	return nil, nil, 0, nil
}
func TestDatabaseCreateJobAndClaim(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	l, e := audit.Open(s, filepath.Join(t.TempDir(), "audit"))
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	seal, e := secret.New(make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	a := agent.New()
	a.Store = s
	a.Audit = l
	a.Jobs = jobs.NewQueue(s, seal, l)
	runner := &databaseRunner{}
	if testingLive := os.Getenv("NOVAPANEL_MARIADB_TEST") == "1"; testingLive {
		a.Exec = exec.Real{}
	} else {
		a.Exec = runner
	}
	server := rpc.NewServer(a)
	reply := server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Role: "admin", Session: "owner"}), rpc.Request{Verb: "database.create", Args: json.RawMessage(`{}`)})
	if !reply.OK {
		t.Fatalf("dispatch: %+v", reply)
	}
	duplicate := server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Role: "admin", Session: "owner"}), rpc.Request{Verb: "database.create", Args: json.RawMessage(`{"name":"same_name"}`)})
	if !duplicate.OK {
		t.Fatalf("first named create: %+v", duplicate)
	}
	duplicate = server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Role: "admin", Session: "owner"}), rpc.Request{Verb: "database.create", Args: json.RawMessage(`{"name":"same_name"}`)})
	if !duplicate.OK {
		t.Fatalf("admission unexpectedly rejected queued duplicate: %+v", duplicate)
	}
	result := reply.Data.(jobs.EnqueueResult)
	var public map[string]any
	if e = json.Unmarshal(result.Job.Args, &public); e != nil || public["name"] == "" || public["username"] == "" || public["password"] != nil {
		t.Fatalf("job must contain public identities but no password: %s (%v)", result.Job.Args, e)
	}
	nonce, ciphertext, e := s.DatabaseIntentSecret(ctx, result.Job.ID)
	if e != nil || len(nonce) == 0 || len(ciphertext) == 0 {
		t.Fatalf("sealed input missing: %v", e)
	}
	opened, e := a.Jobs.OpenInput(&result.Job)
	if e != nil {
		t.Fatal(e)
	}
	var input struct {
		Name string `json:"name"`
		DBID string `json:"db_id"`
	}
	if e = json.Unmarshal(opened, &input); e != nil {
		t.Fatal(e)
	}
	clear(opened)
	if result.Job.ObjectKey != "db:"+input.DBID {
		t.Fatalf("wrong serialization key: %s", result.Job.ObjectKey)
	}
	dbs, e := s.ListDatabases(ctx)
	if e != nil || len(dbs) != 0 {
		t.Fatalf("database published before success: %+v %v", dbs, e)
	}
	j, e := s.StartJob(ctx, result.Job.ID, 1)
	if e != nil {
		t.Fatal(e)
	}
	interrupt := &interruptAfterSchema{delegate: a.Exec}
	a.Exec = interrupt
	if e = server.ExecuteJob(ctx, &j); e == nil || !interrupt.stopped {
		t.Fatal("expected interruption after schema provenance committed")
	}
	a.Exec = interrupt.delegate
	if e = s.RecoverJobs(ctx, 2); e != nil {
		t.Fatal(e)
	}
	j, e = s.StartJob(ctx, j.ID, 2)
	if e != nil {
		t.Fatal(e)
	}
	if e = server.ExecuteJob(ctx, &j); e != nil {
		t.Fatal(e)
	}
	if os.Getenv("NOVAPANEL_MARIADB_TEST") == "1" {
		var result map[string]string
		if e = json.Unmarshal(j.Result, &result); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			_, _, code, err := exec.Real{}.Run(context.Background(), "/usr/bin/mariadb", []string{"--no-defaults", "--protocol=socket", "--user=root"}, exec.RunOpts{Stdin: strings.NewReader("DROP USER IF EXISTS '" + result["db_user"] + "'@'127.0.0.1'; DROP DATABASE IF EXISTS `" + result["db_name"] + "`;")})
			if err != nil || code != 0 {
				t.Errorf("fixture cleanup failed: %v", err)
			}
		})
	}
	fields, e := a.Jobs.Claim(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner"}), j.ID, result.ClaimToken)
	if e != nil || fields["password"] == "" {
		t.Fatalf("claim %v %v", fields, e)
	}
	var created map[string]string
	if e = json.Unmarshal(j.Result, &created); e != nil {
		t.Fatal(e)
	}
	runCredential := func(password string, args ...string) (int, error) {
		t.Helper()
		if os.Geteuid() != 0 || strings.ContainsRune(password, '\x00') {
			t.Fatal("credential fixture requires root and a NUL-free password")
		}
		f, err := os.CreateTemp("", "novapanel-mariadb-*.cnf")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.Remove(f.Name()); err != nil {
				t.Errorf("remove client option file: %v", err)
			}
		}()
		escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`, "\b", `\b`).Replace(password)
		_, writeErr := fmt.Fprintf(f, "[client]\npassword=\"%s\"\n", escaped)
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatalf("write client option file: %v; close: %v", writeErr, closeErr)
		}
		args = append([]string{"--defaults-file=" + f.Name(), "--protocol=tcp", "--host=127.0.0.1", "--user=" + created["db_user"]}, args...)
		_, _, code, err := (exec.Real{}).Run(ctx, "/usr/bin/mariadb", args, exec.RunOpts{})
		return code, err
	}
	connect := func(password string) bool {
		code, err := runCredential(password, "--database="+created["db_name"], "--execute=SELECT 1")
		return err == nil && code == 0
	}
	if os.Getenv("NOVAPANEL_MARIADB_TEST") == "1" {
		if !connect(fields["password"].(string)) {
			t.Fatal("initial credential cannot connect over TCP")
		}
		code, err := runCredential(fields["password"].(string), "--execute=SELECT * FROM mysql.user")
		if err == nil && code == 0 {
			t.Fatal("user can read another database")
		}
		out, _, code, err := (exec.Real{}).Run(ctx, "/usr/bin/mariadb", []string{"--no-defaults", "--protocol=socket", "--user=root", "--batch", "--skip-column-names", "--execute=SHOW GRANTS FOR '" + created["db_user"] + "'@'127.0.0.1'"}, exec.RunOpts{})
		if err != nil || code != 0 {
			t.Fatalf("cannot inspect grants: %v", err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, " ON *.* ") && !strings.HasPrefix(line, "GRANT USAGE ON *.*") {
				t.Fatalf("global privileges: %s", line)
			}
		}
	}
	rotationArgs, _ := json.Marshal(map[string]string{"db_id": created["db_id"]})
	missingArgs, _ := json.Marshal(map[string]string{"db_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
	missing := server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Kind: "user", Role: "admin", Session: "owner", MFAat: time.Now().UnixMilli()}), rpc.Request{Verb: "database.rotate_password", Args: missingArgs})
	if missing.OK || missing.Code != "not_found" || !strings.Contains(missing.Error, "Database") {
		t.Fatalf("missing database: %+v", missing)
	}
	stepped := rpc.Caller{Kind: "user", Role: "admin", Session: "owner", MFAat: time.Now().UnixMilli()}
	denied := server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Kind: "user", Role: "admin", Session: "owner"}), rpc.Request{Verb: "database.rotate_password", Args: rotationArgs})
	if denied.OK {
		t.Fatal("rotation accepted without MFA")
	}
	rotation := server.Dispatch(rpc.WithCaller(ctx, stepped), rpc.Request{Verb: "database.rotate_password", Args: rotationArgs})
	if !rotation.OK {
		t.Fatalf("rotation dispatch: %+v", rotation)
	}
	rr := rotation.Data.(jobs.EnqueueResult)
	rj, e := s.StartJob(ctx, rr.Job.ID, 2)
	if e != nil {
		t.Fatal(e)
	}
	if os.Getenv("NOVAPANEL_MARIADB_TEST") != "1" {
		key := "user:" + created["db_user"] + "@127.0.0.1"
		original := runner.objects[key]
		for _, state := range []string{"created\t1\t01ARZ3NDEKTSV4RRFFQ69G5FAV", "none\t1\t", "reserved\t1\t" + strings.Split(original, "\t")[2], "created\t0\t" + strings.Split(original, "\t")[2]} {
			runner.objects[key] = state
			if err := server.ExecuteJob(ctx, &rj); err == nil {
				t.Fatalf("rotation accepted unproven user %q", state)
			}
		}
		runner.objects[key] = original
	}
	if e = server.ExecuteJob(ctx, &rj); e != nil {
		t.Fatal(e)
	}
	rotated, e := a.Jobs.Claim(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner"}), rj.ID, rr.ClaimToken)
	if e != nil || rotated["password"] == "" || rotated["password"] == fields["password"] {
		t.Fatalf("rotation claim: %v", e)
	}
	if os.Getenv("NOVAPANEL_MARIADB_TEST") == "1" && (!connect(rotated["password"].(string)) || connect(fields["password"].(string))) {
		t.Fatal("rotation did not replace TCP credential")
	}
	if e = s.RecoverJobs(ctx, 2); e != nil {
		t.Fatal(e)
	}
	rj, e = s.StartJob(ctx, rj.ID, 3)
	if e != nil {
		t.Fatal(e)
	}
	callsBeforeReplay := runner.calls
	if e = server.ExecuteJob(ctx, &rj); e != nil || (os.Getenv("NOVAPANEL_MARIADB_TEST") != "1" && runner.calls != callsBeforeReplay) {
		t.Fatalf("rotation replay: %v calls=%d", e, runner.calls)
	}
	if _, e = a.Jobs.Claim(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner"}), rj.ID, rr.ClaimToken); e == nil {
		t.Fatal("rotation claim republished")
	}
	j, e = s.StartJob(ctx, j.ID, 3)
	if e != nil {
		t.Fatal(e)
	}
	callsBeforeReplay = runner.calls
	if e = server.ExecuteJob(ctx, &j); e != nil || (os.Getenv("NOVAPANEL_MARIADB_TEST") != "1" && runner.calls != callsBeforeReplay) {
		t.Fatalf("replay: %v calls=%d", e, runner.calls)
	}
	added := server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Kind: "user", Role: "admin", Session: "owner"}), rpc.Request{Verb: "dbuser.create", Args: rotationArgs})
	if !added.OK {
		t.Fatalf("dbuser create dispatch: %+v", added)
	}
	ar := added.Data.(jobs.EnqueueResult)
	aj, e := s.StartJob(ctx, ar.Job.ID, 4)
	if e != nil {
		t.Fatal(e)
	}
	if e = server.ExecuteJob(ctx, &aj); e != nil {
		t.Fatal(e)
	}
	if claim, err := a.Jobs.Claim(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner"}), aj.ID, ar.ClaimToken); err != nil || claim["password"] == "" {
		t.Fatalf("user claim: %v", err)
	}
	_, users, e := s.DatabaseWithUsers(ctx, created["db_id"])
	if e != nil || len(users) != 2 {
		t.Fatalf("users: %+v %v", users, e)
	}
	rotationWithSecondary := server.Dispatch(rpc.WithCaller(ctx, stepped), rpc.Request{Verb: "database.rotate_password", Args: rotationArgs})
	if !rotationWithSecondary.OK {
		t.Fatalf("owner rotation with secondary account: %+v", rotationWithSecondary)
	}
	var addedResult map[string]string
	if e = json.Unmarshal(aj.Result, &addedResult); e != nil {
		t.Fatal(e)
	}
	userDeleteArgs, _ := json.Marshal(map[string]string{"dbuser_id": addedResult["dbuser_id"]})
	removed := server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Kind: "user", Role: "admin", Session: "owner"}), rpc.Request{Verb: "dbuser.delete", Args: userDeleteArgs})
	if !removed.OK {
		t.Fatalf("dbuser delete dispatch: %+v", removed)
	}
	uj, e := s.StartJob(ctx, removed.Data.(jobs.EnqueueResult).Job.ID, 4)
	if e != nil {
		t.Fatal(e)
	}
	if e = server.ExecuteJob(ctx, &uj); e != nil {
		t.Fatal(e)
	}
	_, users, e = s.DatabaseWithUsers(ctx, created["db_id"])
	if e != nil || len(users) != 1 {
		t.Fatalf("remaining users: %+v %v", users, e)
	}
	if e = server.ExecuteJob(ctx, &uj); e != nil {
		t.Fatalf("dbuser replay: %v", e)
	}
	denied = server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Kind: "user", Role: "admin", Session: "owner"}), rpc.Request{Verb: "database.delete", Args: rotationArgs})
	if denied.OK {
		t.Fatal("delete accepted without MFA")
	}
	deleted := server.Dispatch(rpc.WithCaller(ctx, stepped), rpc.Request{Verb: "database.delete", Args: rotationArgs})
	if !deleted.OK {
		t.Fatalf("delete dispatch: %+v", deleted)
	}
	stale := server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Kind: "user", Role: "admin", Session: "owner"}), rpc.Request{Verb: "dbuser.create", Args: rotationArgs})
	if !stale.OK {
		t.Fatalf("queued child admission: %+v", stale)
	}
	dj, e := s.StartJob(ctx, deleted.Data.(jobs.EnqueueResult).Job.ID, 4)
	if e != nil {
		t.Fatal(e)
	}
	if e = server.ExecuteJob(ctx, &dj); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.DatabaseWithUsers(ctx, created["db_id"]); e == nil {
		t.Fatal("database metadata not removed")
	}
	if e = server.ExecuteJob(ctx, &dj); e != nil {
		t.Fatalf("delete replay: %v", e)
	}
	child, e := s.StartJob(ctx, stale.Data.(jobs.EnqueueResult).Job.ID, 5)
	if e != nil {
		t.Fatal(e)
	}
	if e = server.ExecuteJob(ctx, &child); e == nil || !strings.Contains(e.Error(), "Database no longer exists") {
		t.Fatalf("stale child not rejected: %v", e)
	}
}
