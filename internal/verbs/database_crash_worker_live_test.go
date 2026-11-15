package verbs_test

import (
	"context"
	"encoding/json"
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

type crashWorkerSQL struct {
	delegate exec.Runner
	prefix   string
	contains string
	after    bool
	hit      bool
	cancel   context.CancelFunc
}

func (r *crashWorkerSQL) Run(ctx context.Context, program string, args []string, opts exec.RunOpts) ([]byte, []byte, int, error) {
	if opts.Stdin == nil {
		return r.delegate.Run(ctx, program, args, opts)
	}
	b, err := io.ReadAll(opts.Stdin)
	if err != nil {
		return nil, nil, -1, err
	}
	opts.Stdin = strings.NewReader(string(b))
	match := !r.hit && strings.HasPrefix(string(b), r.prefix) && strings.Contains(string(b), r.contains)
	if match {
		r.hit = true
		if !r.after {
			r.cancel()
			return nil, nil, -1, context.Canceled
		}
	}
	out, stderr, code, err := r.delegate.Run(ctx, program, args, opts)
	if match && err == nil && code == 0 {
		r.cancel()
		return nil, nil, -1, context.Canceled
	}
	return out, stderr, code, err
}

// The worker must leave the durable job running at each simulated process loss;
// recovery and reconciliation then operate on the same SQLite and MariaDB state.
func TestLiveWorkerCreateCrashMatrix(t *testing.T) {
	if os.Getenv("NOVAPANEL_ISOLATION_TEST") != "1" {
		t.Skip("requires disposable MariaDB fixture")
	}
	marker, err := os.ReadFile("/run/novapanel-replay-fixture")
	if os.Getenv("NOVAPANEL_MARIADB_DISPOSABLE") != "network-none" || err != nil || strings.TrimSpace(string(marker)) != "disposable-network-none" {
		t.Fatal("explicit disposable network-none fixture assertion required")
	}
	for _, tc := range []struct {
		name, prefix, contains string
		after, conflict        bool
		user                   bool
	}{
		{"schema_before_reserve", "INSERT INTO novapanel.managed_objects", "'schema'", false, false, false},
		{"schema_after_reserve", "INSERT INTO novapanel.managed_objects", "'schema'", true, false, false},
		{"schema_before_create", "CREATE DATABASE `", "", false, false, false},
		{"schema_after_create", "CREATE DATABASE `", "", true, true, false},
		{"schema_before_created", "UPDATE novapanel.managed_objects SET phase='created'", "kind='schema'", false, true, false},
		{"schema_after_created", "UPDATE novapanel.managed_objects SET phase='created'", "kind='schema'", true, false, false},
		{"user_before_reserve", "INSERT INTO novapanel.managed_objects", "'user'", false, false, true},
		{"user_after_reserve", "INSERT INTO novapanel.managed_objects", "'user'", true, false, true},
		{"user_before_create", "CREATE USER '", "", false, false, true},
		{"user_after_create", "CREATE USER '", "", true, true, true},
		{"user_before_created", "UPDATE novapanel.managed_objects SET phase='created'", "kind='user'", false, true, true},
		{"user_after_created", "UPDATE novapanel.managed_objects SET phase='created'", "kind='user'", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			args, _ := json.Marshal(map[string]string{"name": name})
			caller := rpc.Caller{Kind: "user", Role: "admin", Session: "owner", MFAat: time.Now().UnixMilli()}
			reply := server.Dispatch(rpc.WithCaller(ctx, caller), rpc.Request{Verb: "database.create", Args: args})
			if !reply.OK {
				t.Fatalf("enqueue: %+v", reply)
			}
			en := reply.Data.(jobs.EnqueueResult)
			var intent struct {
				DBID     string `json:"db_id"`
				UserID   string `json:"user_id"`
				Username string `json:"username"`
			}
			if err := json.Unmarshal(en.Job.Args, &intent); err != nil || intent.DBID == "" || intent.UserID == "" || intent.Username == "" {
				t.Fatalf("invalid durable job identity: %v", err)
			}
			defer func() {
				sql := fmt.Sprintf("DROP USER IF EXISTS '%s'@'127.0.0.1'; DROP DATABASE IF EXISTS `%s`; DELETE FROM novapanel.managed_objects WHERE (kind='schema' AND name='%s' AND obj_id='%s') OR (kind='user' AND name='%s@127.0.0.1' AND obj_id='%s');", intent.Username, name, name, intent.DBID, intent.Username, intent.UserID)
				_, stderr, code, err := (exec.Real{}).Run(ctx, "/usr/bin/mariadb", []string{"--no-defaults", "--protocol=socket", "--user=root", "--batch"}, exec.RunOpts{Stdin: strings.NewReader(sql)})
				if err != nil || code != 0 {
					t.Errorf("fixture cleanup: %v (%d): %s", err, code, stderr)
				}
			}()
			wc, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			stop := &crashWorkerSQL{delegate: exec.Real{}, prefix: tc.prefix, contains: tc.contains, after: tc.after, cancel: cancel}
			a.Exec = stop
			if err = a.Jobs.Run(wc); err != nil {
				t.Fatal(err)
			}
			if !stop.hit {
				j, _ := s.GetJob(ctx, en.Job.ID)
				t.Fatalf("injection not reached: %+v", j)
			}
			j, err := s.GetJob(ctx, en.Job.ID)
			if err != nil || j.State != "running" {
				t.Fatalf("interrupted job: %+v %v", j, err)
			}
			a.Exec = exec.Real{}
			if _, err = database.Reconcile(ctx, a.Exec, s, a.Jobs.LockReconcileDatabase); err != nil {
				t.Fatalf("reconcile live job: %v", err)
			}
			wc, cancel = context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- a.Jobs.Run(wc) }()
			deadline := time.Now().Add(30 * time.Second)
			for {
				j, err = s.GetJob(ctx, en.Job.ID)
				if err != nil {
					t.Fatal(err)
				}
				if j.State == "done" || j.State == "failed" {
					break
				}
				if time.Now().After(deadline) {
					cancel()
					<-done
					t.Fatal("replay timed out")
				}
				time.Sleep(10 * time.Millisecond)
			}
			cancel()
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			if tc.conflict {
				if j.State != "failed" || j.Code != "conflict" {
					t.Fatalf("expected conflict: %+v", j)
				}
				if _, err = database.Reconcile(ctx, a.Exec, s, a.Jobs.LockReconcileDatabase); err != database.ErrConflict {
					t.Fatalf("expected reconcile conflict: %v", err)
				}
			} else {
				if j.State != "done" {
					t.Fatalf("replay: %+v", j)
				}
				if j.Step != "database_committed" {
					t.Fatalf("SQLite publication checkpoint missing: %+v", j)
				}
				if _, err = database.Reconcile(ctx, a.Exec, s, a.Jobs.LockReconcileDatabase); err != nil {
					t.Fatalf("reconcile: %v", err)
				}
			}
			query := fmt.Sprintf("SELECT (SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s'),COALESCE((SELECT phase FROM novapanel.managed_objects WHERE kind='schema' AND name='%s' AND obj_id='%s'),'none');", name, name, intent.DBID)
			if tc.user {
				query = fmt.Sprintf("SELECT (SELECT COUNT(*) FROM mysql.user WHERE User='%s' AND Host='127.0.0.1'),COALESCE((SELECT phase FROM novapanel.managed_objects WHERE kind='user' AND name='%s@127.0.0.1' AND obj_id='%s'),'none');", intent.Username, intent.Username, intent.UserID)
			}
			out, stderr, code, err := (exec.Real{}).Run(ctx, "/usr/bin/mariadb", []string{"--no-defaults", "--protocol=socket", "--user=root", "--batch", "--skip-column-names"}, exec.RunOpts{Stdin: strings.NewReader(query)})
			want := "1\tcreated"
			if tc.conflict {
				want = "1\treserved"
			}
			if err != nil || code != 0 || strings.TrimSpace(string(out)) != want {
				t.Fatalf("external state: got %q want %q: %v (%d): %s", out, want, err, code, stderr)
			}
		})
	}
}
