package verbs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/database"
	"novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

type liveDeleteFault struct {
	hit  bool
	drop string
}

func (r *liveDeleteFault) Run(ctx context.Context, p string, a []string, o exec.RunOpts) ([]byte, []byte, int, error) {
	if o.Stdin == nil {
		return (exec.Real{}).Run(ctx, p, a, o)
	}
	b, err := io.ReadAll(o.Stdin)
	if err != nil {
		return nil, nil, -1, err
	}
	o.Stdin = strings.NewReader(string(b))
	if !r.hit && strings.HasPrefix(string(b), r.drop) {
		r.hit = true
		return nil, []byte("injected transient SQL failure"), 1, errors.New("injected transient SQL failure")
	}
	return (exec.Real{}).Run(ctx, p, a, o)
}

func TestLiveQueueDeleteTransientAfterMarker(t *testing.T) {
	if os.Getenv("NOVAPANEL_ISOLATION_TEST") != "1" {
		t.Skip("requires disposable MariaDB fixture")
	}
	marker, err := os.ReadFile("/run/novapanel-replay-fixture")
	if os.Getenv("NOVAPANEL_MARIADB_DISPOSABLE") != "network-none" || err != nil || strings.TrimSpace(string(marker)) != "disposable-network-none" {
		t.Fatal("disposable fixture required")
	}
	for _, tc := range []struct{ name, verb, drop string }{
		{"secondary_user", "dbuser.delete", "DROP USER"},
		{"cascade_user", "database.delete", "DROP USER"},
		{"schema", "database.delete", "DROP DATABASE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verb := tc.verb
			ctx := context.Background()
			s, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
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
			if err := database.Bootstrap(ctx, a.Exec); err != nil {
				t.Fatalf("bootstrap: %v", err)
			}
			server := rpc.NewServer(a)
			server.ConfigureJobs()
			caller := rpc.Caller{Kind: "user", Role: "admin", Session: "owner", MFAat: time.Now().UnixMilli()}
			dispatch := func(verb string, args any) jobs.EnqueueResult {
				t.Helper()
				raw, e := json.Marshal(args)
				if e != nil {
					t.Fatal(e)
				}
				reply := server.Dispatch(rpc.WithCaller(ctx, caller), rpc.Request{Verb: verb, Args: raw})
				if !reply.OK {
					t.Fatalf("%s: %+v", verb, reply)
				}
				return reply.Data.(jobs.EnqueueResult)
			}
			runSQL := func(query string) string {
				t.Helper()
				out, stderr, code, e := (exec.Real{}).Run(ctx, "/usr/bin/mariadb", []string{"--no-defaults", "--protocol=socket", "--user=root", "--batch", "--skip-column-names"}, exec.RunOpts{Stdin: strings.NewReader(query)})
				if e != nil || code != 0 {
					t.Fatalf("SQL: %v %s", e, stderr)
				}
				return strings.TrimSpace(string(out))
			}
			suffix, err := id.New()
			if err != nil {
				t.Fatal(err)
			}
			suffix = strings.ToLower(suffix[12:])
			name, foreign, foreignUser := "rt_"+suffix, "foreign_"+suffix, "fu_"+suffix
			runSQL(fmt.Sprintf("CREATE DATABASE `%s`; CREATE USER '%s'@'127.0.0.1';", foreign, foreignUser))
			t.Cleanup(func() {
				runSQL(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`; DROP USER IF EXISTS '%s'@'127.0.0.1';", foreign, foreignUser))
			})
			workerCtx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- a.Jobs.Run(workerCtx) }()
			defer func() {
				cancel()
				if done != nil {
					if e := <-done; e != nil {
						t.Error(e)
					}
				}
			}()
			wait := func(job string) store.Job {
				t.Helper()
				deadline := time.Now().Add(30 * time.Second)
				for {
					j, e := s.GetJob(ctx, job)
					if e != nil {
						t.Fatal(e)
					}
					if j.State == "done" || j.State == "failed" {
						return j
					}
					if time.Now().After(deadline) {
						t.Fatal("worker timeout")
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			created := dispatch("database.create", map[string]string{"name": name})
			if j := wait(created.Job.ID); j.State != "done" {
				t.Fatalf("create: %+v", j)
			}
			var intent struct {
				DBID     string `json:"db_id"`
				UserID   string `json:"user_id"`
				Username string `json:"username"`
			}
			if err := json.Unmarshal(created.Job.Args, &intent); err != nil {
				t.Fatal(err)
			}
			if verb == "dbuser.delete" {
				added := dispatch("dbuser.create", map[string]string{"db_id": intent.DBID})
				if j := wait(added.Job.ID); j.State != "done" {
					t.Fatalf("create user: %+v", j)
				}
				var user struct {
					ID       string `json:"user_id"`
					Username string `json:"username"`
				}
				if err := json.Unmarshal(added.Job.Args, &user); err != nil {
					t.Fatal(err)
				}
				intent.UserID, intent.Username = user.ID, user.Username
			}
			cancel()
			if err := <-done; err != nil {
				done = nil
				t.Fatal(err)
			}
			done = make(chan error, 1)
			workerCtx, cancel = context.WithCancel(ctx)
			defer cancel()
			targetID, targetName, kind := intent.UserID, intent.Username+"@127.0.0.1", "user"
			args := map[string]string{"dbuser_id": intent.UserID}
			if verb == "database.delete" {
				args = map[string]string{"db_id": intent.DBID}
			}
			if tc.name == "schema" {
				targetID, targetName, kind = intent.DBID, name, "schema"
			}
			fault := &liveDeleteFault{drop: tc.drop}
			a.Exec = fault
			attempts := make(chan error, 2)
			resume := make(chan struct{})
			a.Jobs.SetRunner(func(c context.Context, j *jobs.Job) error {
				e := server.ExecuteJob(c, j)
				if j.Verb == verb {
					attempts <- e
					if errors.Is(e, jobs.ErrRetry) {
						select {
						case <-resume:
						case <-c.Done():
						}
					}
				}
				return e
			})
			go func() { done <- a.Jobs.Run(workerCtx) }()
			deleted := dispatch(verb, args)
			select {
			case e := <-attempts:
				if !errors.Is(e, jobs.ErrRetry) {
					t.Fatalf("first attempt: %v", e)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("no first attempt")
			}
			phase := runSQL(fmt.Sprintf("SELECT phase FROM novapanel.managed_objects WHERE kind='%s' AND name='%s' AND obj_id='%s';", kind, targetName, targetID))
			if phase != "deleting" || !fault.hit {
				t.Fatalf("marker=%q fault=%t", phase, fault.hit)
			}
			objectQuery := fmt.Sprintf("SELECT COUNT(*) FROM mysql.user WHERE User='%s' AND Host='127.0.0.1';", intent.Username)
			if kind == "schema" {
				objectQuery = fmt.Sprintf("SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s';", name)
			}
			if got := runSQL(objectQuery); got != "1" {
				t.Fatalf("object absent before retry: %s", got)
			}
			if got := runSQL(fmt.Sprintf("SELECT (SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s'),(SELECT COUNT(*) FROM mysql.user WHERE User='%s' AND Host='127.0.0.1');", foreign, foreignUser)); got != "1\t1" {
				t.Fatalf("foreign objects at stop: %q", got)
			}
			close(resume)
			select {
			case e := <-attempts:
				if e != nil {
					t.Fatalf("retry: %v", e)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("no retry")
			}
			if j := wait(deleted.Job.ID); j.State != "done" {
				t.Fatalf("delete: %+v", j)
			}
			if got := runSQL(fmt.Sprintf("SELECT COUNT(*) FROM novapanel.managed_objects WHERE kind='%s' AND name='%s' AND obj_id='%s';", kind, targetName, targetID)); got != "0" {
				t.Fatalf("delete retained provenance marker: %q", got)
			}
			if verb == "database.delete" {
				if got := runSQL(fmt.Sprintf("SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s';", name)); got != "0" {
					t.Fatalf("target schema retained: %s", got)
				}
			}
			if verb == "dbuser.delete" {
				_, err = s.DatabaseUserByID(ctx, intent.UserID)
			} else {
				_, _, err = s.DatabaseWithUsers(ctx, intent.DBID)
			}
			if !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("SQLite owner retained: %v", err)
			}
			got := runSQL(fmt.Sprintf("SELECT (SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s'),(SELECT COUNT(*) FROM mysql.user WHERE User='%s' AND Host='127.0.0.1'),(SELECT COUNT(*) FROM mysql.user WHERE User='%s' AND Host='127.0.0.1');", foreign, foreignUser, intent.Username))
			if got != "1\t1\t0" {
				t.Fatalf("foreign or target state: %q", got)
			}
		})
	}
}
