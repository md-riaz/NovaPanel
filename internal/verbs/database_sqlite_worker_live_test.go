package verbs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

// The wrapper stops the actual worker immediately around durable SQLite writes.
type boundaryStore struct {
	*store.Store
	target, boundary string
	after, hit       bool
	failPublish      bool
	cancel           context.CancelFunc
}

func (s *boundaryStore) stop(boundary, job string, after bool) bool {
	if s.hit || s.boundary != boundary || s.target != job || s.after != after {
		return false
	}
	s.hit = true
	s.cancel()
	return true
}
func (s *boundaryStore) CommitDatabaseClaim(ctx context.Context, job, verb, key string, result json.RawMessage, nonce, ciphertext []byte) error {
	if s.stop("publish", job, false) {
		return context.Canceled
	}
	if s.failPublish && s.target == job {
		return errors.New("injected publication failure")
	}
	err := s.Store.CommitDatabaseClaim(ctx, job, verb, key, result, nonce, ciphertext)
	if err == nil {
		s.stop("publish", job, true)
	}
	return err
}
func (s *boundaryStore) UpdateJob(ctx context.Context, j store.Job) error {
	terminal := j.State == "done" || j.State == "failed"
	if terminal && s.stop("terminal", j.ID, false) {
		return context.Canceled
	}
	err := s.Store.UpdateJob(ctx, j)
	if terminal && err == nil {
		s.stop("terminal", j.ID, true)
	}
	return err
}

func TestLiveWorkerSQLiteCrashBoundaries(t *testing.T) {
	if os.Getenv("NOVAPANEL_ISOLATION_TEST") != "1" {
		t.Skip("requires disposable MariaDB fixture")
	}
	marker, err := os.ReadFile("/run/novapanel-replay-fixture")
	if os.Getenv("NOVAPANEL_MARIADB_DISPOSABLE") != "network-none" || err != nil || strings.TrimSpace(string(marker)) != "disposable-network-none" {
		t.Fatal("disposable fixture required")
	}
	for _, boundary := range []string{"publish", "terminal", "delete-terminal", "delete-user-mark", "delete-user-drop", "delete-schema-mark", "delete-schema-drop", "abort-user-mark", "abort-schema-mark", "abort-user-drop", "abort-schema-drop", "abort-terminal"} {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_after_%t", boundary, after), func(t *testing.T) {
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
				server := rpc.NewServer(a)
				server.ConfigureJobs()
				ident, err := id.New()
				if err != nil {
					t.Fatal(err)
				}
				name := "cr_" + strings.ToLower(ident[8:])
				caller := rpc.Caller{Kind: "user", Role: "admin", Session: "owner", MFAat: time.Now().UnixMilli()}
				dispatch := func(verb string, args any) jobs.EnqueueResult {
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
				created := dispatch("database.create", map[string]string{"name": name})
				var intent struct {
					DBID     string `json:"db_id"`
					UserID   string `json:"user_id"`
					Username string `json:"username"`
				}
				if err := json.Unmarshal(created.Job.Args, &intent); err != nil {
					t.Fatal(err)
				}
				var secondary []struct{ ID, Username string }
				defer func() {
					query := fmt.Sprintf("DROP USER IF EXISTS '%s'@'127.0.0.1'; DROP DATABASE IF EXISTS `%s`; DELETE FROM novapanel.managed_objects WHERE (kind='schema' AND name='%s' AND obj_id='%s') OR (kind='user' AND name='%s@127.0.0.1' AND obj_id='%s');", intent.Username, name, name, intent.DBID, intent.Username, intent.UserID)
					for _, u := range secondary {
						query += fmt.Sprintf("DROP USER IF EXISTS '%s'@'127.0.0.1'; DELETE FROM novapanel.managed_objects WHERE kind='user' AND name='%s@127.0.0.1' AND obj_id='%s';", u.Username, u.Username, u.ID)
					}
					_, stderr, code, e := (exec.Real{}).Run(ctx, "/usr/bin/mariadb", []string{"--no-defaults", "--protocol=socket", "--user=root", "--batch"}, exec.RunOpts{Stdin: strings.NewReader(query)})
					if e != nil || code != 0 {
						t.Errorf("cleanup %v %s", e, stderr)
					}
				}()
				runUntil := func(job string) store.Job {
					t.Helper()
					c, cancel := context.WithCancel(ctx)
					done := make(chan error, 1)
					go func() { done <- a.Jobs.Run(c) }()
					deadline := time.Now().Add(30 * time.Second)
					for {
						j, e := s.GetJob(ctx, job)
						if e != nil {
							t.Fatal(e)
						}
						if j.State == "done" || j.State == "failed" {
							cancel()
							if e = <-done; e != nil {
								t.Fatal(e)
							}
							return j
						}
						if time.Now().After(deadline) {
							cancel()
							<-done
							t.Fatal("worker timed out")
						}
						time.Sleep(10 * time.Millisecond)
					}
				}
				target := created.Job.ID
				if strings.HasPrefix(boundary, "delete-") {
					j := runUntil(target)
					if j.State != "done" {
						t.Fatalf("create: %+v", j)
					}
					for i := 0; i < 2; i++ {
						added := dispatch("dbuser.create", map[string]string{"db_id": intent.DBID})
						var u struct {
							ID       string `json:"user_id"`
							Username string `json:"username"`
						}
						if err := json.Unmarshal(added.Job.Args, &u); err != nil {
							t.Fatal(err)
						}
						secondary = append(secondary, struct{ ID, Username string }{u.ID, u.Username})
						if j := runUntil(added.Job.ID); j.State != "done" {
							t.Fatalf("create secondary: %+v", j)
						}
					}
					target = dispatch("database.delete", map[string]string{"db_id": intent.DBID}).Job.ID
				}
				c, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				point := boundary
				if point == "delete-terminal" || point == "abort-terminal" {
					point = "terminal"
				}
				wrapped := &boundaryStore{Store: s, target: target, boundary: point, after: after, cancel: cancel}
				wrapped.failPublish = strings.HasPrefix(boundary, "abort-")
				a.Jobs = jobs.NewQueue(wrapped, seal, log)
				a.Jobs.SetRunner(server.ExecuteJob)
				var stop *crashWorkerSQL
				if (strings.HasPrefix(boundary, "delete-") || strings.HasPrefix(boundary, "abort-")) && point != "terminal" {
					prefix, contains := "UPDATE novapanel.managed_objects SET phase='deleting'", "kind='user'"
					if strings.Contains(boundary, "schema") {
						contains = "kind='schema'"
					}
					if strings.HasSuffix(boundary, "drop") {
						contains, prefix = "", "DROP USER IF EXISTS '"
						if strings.Contains(boundary, "schema") {
							prefix = "DROP DATABASE IF EXISTS `"
						}
					}
					stop = &crashWorkerSQL{delegate: exec.Real{}, prefix: prefix, contains: contains, after: after, cancel: cancel}
					a.Exec = stop
				}
				if e := a.Jobs.Run(c); e != nil {
					t.Fatal(e)
				}
				if (stop == nil && !wrapped.hit) || (stop != nil && !stop.hit) {
					t.Fatal("boundary not reached")
				}
				a.Exec = exec.Real{}
				j, e := s.GetJob(ctx, target)
				if e != nil {
					t.Fatal(e)
				}
				if after && point == "terminal" {
					want := "done"
					if strings.HasPrefix(boundary, "abort-") {
						want = "failed"
					}
					if j.State != want {
						t.Fatalf("terminal not committed: %+v", j)
					}
				} else if j.State != "running" {
					t.Fatalf("not interrupted: %+v", j)
				}
				if !(boundary == "delete-terminal" && after) {
					if _, e = database.Reconcile(ctx, a.Exec, s, a.Jobs.LockReconcileDatabase); e != nil {
						t.Fatalf("reconcile interrupted: %v", e)
					}
				}
				j = runUntil(target)
				wantState := "done"
				if strings.HasPrefix(boundary, "abort-") {
					wantState = "failed"
				}
				if j.State != wantState {
					t.Fatalf("replay: %+v", j)
				}
				if boundary == "delete-terminal" {
					rc, stopReconcile := context.WithCancel(ctx)
					stop := &crashWorkerSQL{delegate: exec.Real{}, prefix: "DELETE FROM novapanel.managed_objects", after: after, cancel: stopReconcile}
					_, _ = database.Reconcile(rc, stop, s, a.Jobs.LockReconcileDatabase)
					stopReconcile()
					if !stop.hit {
						t.Fatal("reconcile marker removal boundary not reached")
					}
				}
				if _, e = database.Reconcile(ctx, a.Exec, s, a.Jobs.LockReconcileDatabase); e != nil {
					t.Fatalf("reconcile terminal: %v", e)
				}
				_, users, e := s.DatabaseWithUsers(ctx, intent.DBID)
				if strings.HasPrefix(boundary, "delete-") || strings.HasPrefix(boundary, "abort-") {
					if e != sql.ErrNoRows {
						t.Fatalf("database retained: %v", e)
					}
				} else if e != nil || len(users) != 1 {
					t.Fatalf("publication missing: %v %+v", e, users)
				}
				query := fmt.Sprintf("SELECT (SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s'),(SELECT COUNT(*) FROM mysql.user WHERE User='%s' AND Host='127.0.0.1'),(SELECT COUNT(*) FROM novapanel.managed_objects WHERE (kind='schema' AND name='%s' AND obj_id='%s') OR (kind='user' AND name='%s@127.0.0.1' AND obj_id='%s'));", name, intent.Username, name, intent.DBID, intent.Username, intent.UserID)
				out, stderr, code, e := (exec.Real{}).Run(ctx, "/usr/bin/mariadb", []string{"--no-defaults", "--protocol=socket", "--user=root", "--batch", "--skip-column-names"}, exec.RunOpts{Stdin: strings.NewReader(query)})
				want := "1\t1\t2"
				if strings.HasPrefix(boundary, "delete-") || strings.HasPrefix(boundary, "abort-") {
					want = "0\t0\t0"
				}
				if e != nil || code != 0 || strings.TrimSpace(string(out)) != want {
					t.Fatalf("external %q want %q: %v %s", out, want, e, stderr)
				}
				for _, u := range secondary {
					query = fmt.Sprintf("SELECT (SELECT COUNT(*) FROM mysql.user WHERE User='%s' AND Host='127.0.0.1'),(SELECT COUNT(*) FROM novapanel.managed_objects WHERE kind='user' AND name='%s@127.0.0.1' AND obj_id='%s');", u.Username, u.Username, u.ID)
					out, stderr, code, e = (exec.Real{}).Run(ctx, "/usr/bin/mariadb", []string{"--no-defaults", "--protocol=socket", "--user=root", "--batch", "--skip-column-names"}, exec.RunOpts{Stdin: strings.NewReader(query)})
					if e != nil || code != 0 || strings.TrimSpace(string(out)) != "0\t0" {
						t.Fatalf("secondary %s retained: %q %v %s", u.Username, out, e, stderr)
					}
				}
			})
		}
	}
}
