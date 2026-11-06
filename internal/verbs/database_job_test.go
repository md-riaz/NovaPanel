package verbs_test

import (
	"context"
	"encoding/json"
	"errors"
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
	cancel   context.CancelFunc
}

func (r *interruptAfterSchema) Run(ctx context.Context, program string, args []string, o exec.RunOpts) ([]byte, []byte, int, error) {
	b, err := io.ReadAll(o.Stdin)
	if err != nil {
		return nil, nil, -1, err
	}
	o.Stdin = strings.NewReader(string(b))
	if !r.stopped && strings.Contains(string(b), "INSERT INTO novapanel.managed_objects") && strings.Contains(string(b), "'user'") {
		r.stopped = true
		r.cancel()
		return nil, nil, -1, context.Canceled
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

type recordingDatabaseRunner struct {
	*databaseRunner
	statements []string
}

func (r *recordingDatabaseRunner) Run(ctx context.Context, program string, args []string, o exec.RunOpts) ([]byte, []byte, int, error) {
	b, err := io.ReadAll(o.Stdin)
	if err != nil {
		return nil, nil, -1, err
	}
	r.statements = append(r.statements, string(b))
	o.Stdin = strings.NewReader(string(b))
	return r.databaseRunner.Run(ctx, program, args, o)
}

type foreignUserRunner struct {
	exec.Runner
	statements                     []string
	username, userID, owner, phase string
	present                        bool
}

func (r *foreignUserRunner) Run(ctx context.Context, program string, args []string, opts exec.RunOpts) ([]byte, []byte, int, error) {
	b, err := io.ReadAll(opts.Stdin)
	if err != nil {
		return nil, nil, -1, err
	}
	r.statements = append(r.statements, string(b))
	s := string(b)
	if strings.HasPrefix(s, "SELECT COALESCE((SELECT phase FROM novapanel.managed_objects") {
		expected := fmt.Sprintf("SELECT COALESCE((SELECT phase FROM novapanel.managed_objects WHERE kind='user' AND name='%s@127.0.0.1' AND obj_id='%s'),'none');", r.username, r.userID)
		if s != expected {
			return nil, nil, 1, fmt.Errorf("unexpected lookup: %s", s)
		}
		if r.owner == r.userID {
			return []byte(r.phase + "\n"), nil, 0, nil
		}
		return []byte("none\n"), nil, 0, nil
	}
	if strings.HasPrefix(s, "SELECT COUNT(*) FROM mysql.user") {
		if s != fmt.Sprintf("SELECT COUNT(*) FROM mysql.user WHERE User='%s' AND Host='127.0.0.1';", r.username) {
			return nil, nil, 1, errors.New("unexpected user lookup")
		}
		if r.present {
			return []byte("1\n"), nil, 0, nil
		}
		return []byte("0\n"), nil, 0, nil
	}
	if strings.HasPrefix(s, "DELETE ") || strings.HasPrefix(s, "DROP ") || strings.HasPrefix(s, "CREATE USER") || strings.HasPrefix(s, "ALTER USER") || strings.HasPrefix(s, "GRANT ") {
		return nil, nil, 1, errors.New("unexpected mutation: " + s)
	}
	opts.Stdin = strings.NewReader(string(b))
	return r.Runner.Run(ctx, program, args, opts)
}

func TestDatabaseCreateReservedPresentDoesNotAdoptOrDrop(t *testing.T) {
	ctx := context.Background()
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
	a.Store, a.Audit, a.Jobs = s, log, jobs.NewQueue(s, seal, log)
	runner := &recordingDatabaseRunner{databaseRunner: &databaseRunner{objects: make(map[string]string)}}
	a.Exec = runner
	server := rpc.NewServer(a)
	reply := server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Role: "admin", Session: "owner"}), rpc.Request{Verb: "database.create", Args: json.RawMessage(`{"name":"d_foreign"}`)})
	if !reply.OK {
		t.Fatalf("admission: %+v", reply)
	}
	job, err := s.StartJob(ctx, reply.Data.(jobs.EnqueueResult).Job.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		DBID string `json:"db_id"`
	}
	if err := json.Unmarshal(job.Args, &input); err != nil || input.DBID == "" {
		t.Fatalf("job args: %s: %v", job.Args, err)
	}
	key := "schema:d_foreign"
	state := "reserved\t1\t" + input.DBID
	runner.objects[key] = state
	err = server.ExecuteJob(ctx, &job)
	var coded *rpc.CodedError
	if !errors.As(err, &coded) || coded.Code != "conflict" {
		t.Fatalf("expected conflict, got %v", err)
	}
	if runner.objects[key] != state {
		t.Fatalf("reservation changed: %q", runner.objects[key])
	}
	for _, statement := range runner.statements {
		if strings.HasPrefix(statement, "DROP ") || strings.HasPrefix(statement, "CREATE DATABASE `d_foreign`") || strings.HasPrefix(statement, "DELETE FROM novapanel.managed_objects") || strings.Contains(statement, "SET phase='created'") || strings.Contains(statement, "SET phase='deleting'") {
			t.Fatalf("unproven object mutated: %s", statement)
		}
	}
	dbs, err := s.ListDatabases(ctx)
	if err != nil || len(dbs) != 0 {
		t.Fatalf("published database: %+v %v", dbs, err)
	}
}

func TestDatabaseCreateUnknownSiteHasNoExternalEffects(t *testing.T) {
	ctx := context.Background()
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
	a.Jobs = jobs.NewQueue(s, seal, log)
	runner := &databaseRunner{}
	a.Exec = runner
	server := rpc.NewServer(a)
	reply := server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Role: "admin", Session: "owner"}), rpc.Request{Verb: "database.create", Args: json.RawMessage(`{"site_id":"01ARZ3NDEKTSV4RRFFQ69G5FAV"}`)})
	if !reply.OK {
		t.Fatalf("admission: %+v", reply)
	}
	job, err := s.StartJob(ctx, reply.Data.(jobs.EnqueueResult).Job.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	err = server.ExecuteJob(ctx, &job)
	var coded *rpc.CodedError
	if !errors.As(err, &coded) || coded.Code != "not_found" || runner.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, runner.calls)
	}
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
	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	interrupt := &interruptAfterSchema{delegate: a.Exec, cancel: cancel}
	a.Exec = interrupt
	if e = server.ExecuteJob(attemptCtx, &j); !errors.Is(e, context.Canceled) || !interrupt.stopped {
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
			// Each refusal represents a separate first attempt, not a replay of a
			// prior checkpoint whose ALTER outcome may be ambiguous.
			rj.Step = ""
			if err := s.UpdateJob(ctx, rj); err != nil {
				t.Fatal(err)
			}
			if err := server.ExecuteJob(ctx, &rj); err == nil {
				t.Fatalf("rotation accepted unproven user %q", state)
			}
		}
		runner.objects[key] = original
		rj.Step = ""
		if err := s.UpdateJob(ctx, rj); err != nil {
			t.Fatal(err)
		}
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
	if len(addedResult) != 2 || addedResult["dbuser_id"] == "" || addedResult["db_user"] == "" || addedResult["db_user"] != users[1].Username {
		t.Fatalf("non-secret user result: %+v", addedResult)
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
	// The same-name marker belongs to another resource, as does the account.
	var childInput struct {
		Username string `json:"username"`
		UserID   string `json:"user_id"`
	}
	if err := json.Unmarshal(child.Args, &childInput); err != nil || childInput.Username == "" || childInput.UserID == "" {
		t.Fatalf("child args: %s: %v", child.Args, err)
	}
	foreign := &foreignUserRunner{Runner: a.Exec, username: childInput.Username, userID: childInput.UserID, owner: "01ARZ3NDEKTSV4RRFFQ69G5FAW", phase: "reserved", present: true}
	a.Exec = foreign
	e = server.ExecuteJob(ctx, &child)
	var coded *rpc.CodedError
	if !errors.As(e, &coded) || coded.Code != "precondition_failed" {
		t.Fatalf("stale child not rejected with precondition_failed: %v", e)
	}
	if !foreign.present || foreign.owner != "01ARZ3NDEKTSV4RRFFQ69G5FAW" || foreign.phase != "reserved" {
		t.Fatalf("foreign state changed: %+v", foreign)
	}
	lookups := 0
	for _, statement := range foreign.statements {
		if strings.HasPrefix(statement, "SELECT COALESCE((SELECT phase FROM novapanel.managed_objects") {
			lookups++
		}
		if strings.HasPrefix(statement, "DROP ") || strings.HasPrefix(statement, "DELETE FROM novapanel.managed_objects") || strings.HasPrefix(statement, "CREATE USER") || strings.HasPrefix(statement, "ALTER USER") || strings.HasPrefix(statement, "GRANT ") {
			t.Fatalf("stale child modified foreign state: %s", statement)
		}
	}
	if lookups != 1 {
		t.Fatalf("expected one identity-scoped lookup, got %d", lookups)
	}
}
