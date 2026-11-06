package verbs_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

// A SQLite-only competing username appears after admission, when MariaDB is still free.
func TestCreatePublishUserConstraintQueue(t *testing.T) {
	for _, verb := range []string{"database.create", "dbuser.create"} {
		t.Run(verb, func(t *testing.T) {
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
			runner := &recordingDatabaseRunner{databaseRunner: &databaseRunner{}}
			a := agent.New()
			a.Store, a.Audit, a.Exec = s, log, runner
			a.Jobs = jobs.NewQueue(s, seal, log)
			server := rpc.NewServer(a)
			caller := rpc.WithCaller(ctx, rpc.Caller{Kind: "user", Role: "admin", Session: "owner", MFAat: time.Now().UnixMilli()})
			dispatch := func(v string, args any) jobs.EnqueueResult {
				t.Helper()
				b, _ := json.Marshal(args)
				r := server.Dispatch(caller, rpc.Request{Verb: v, Args: b})
				if !r.OK {
					t.Fatalf("admission: %+v", r)
				}
				return r.Data.(jobs.EnqueueResult)
			}
			var en jobs.EnqueueResult
			if verb == "database.create" {
				en = dispatch(verb, map[string]string{"name": "targetdb"})
			} else {
				parent := dispatch("database.create", map[string]string{"name": "targetdb"})
				j, e := s.StartJob(ctx, parent.Job.ID, 1)
				if e != nil {
					t.Fatal(e)
				}
				if e = server.ExecuteJob(ctx, &j); e != nil {
					t.Fatal(e)
				}
				j.State = "done"
				if e = s.UpdateJob(ctx, j); e != nil {
					t.Fatal(e)
				}
				var p struct {
					DBID string `json:"db_id"`
				}
				if e = json.Unmarshal(j.Result, &p); e != nil {
					t.Fatal(e)
				}
				en = dispatch(verb, map[string]string{"db_id": p.DBID, "username": "collision"})
			}
			var intent struct {
				Username string `json:"username"`
				DBID     string `json:"db_id"`
				UserID   string `json:"user_id"`
			}
			if err = json.Unmarshal(en.Job.Args, &intent); err != nil {
				t.Fatal(err)
			}
			d := store.Database{ID: "other-db", Name: "otherdb", Engine: "mariadb"}
			u := store.DatabaseUser{ID: "other-user", DatabaseID: d.ID, Username: intent.Username}
			if err = s.ReserveDatabase(ctx, d, u); err != nil {
				t.Fatal(err)
			}
			worker, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			a.Jobs.SetRunner(server.ExecuteJob)
			go func() { done <- a.Jobs.Run(worker) }()
			defer func() { cancel(); <-done }()
			deadline := time.After(5 * time.Second)
			for {
				j, e := s.GetJob(ctx, en.Job.ID)
				if e != nil {
					t.Fatal(e)
				}
				if j.State == "failed" {
					if j.Code != "conflict" {
						t.Fatalf("code %s", j.Code)
					}
					break
				}
				if j.State == "done" {
					t.Fatal("unexpected success")
				}
				select {
				case <-deadline:
					t.Fatal("timeout")
				case <-time.After(10 * time.Millisecond):
				}
			}
			if _, _, err = s.DatabaseWithUsers(ctx, d.ID); err != nil {
				t.Fatalf("competitor lost: %v", err)
			}
			if _, err = a.Jobs.Claim(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner"}), en.Job.ID, en.ClaimToken); err == nil {
				t.Fatal("failed claim published")
			}
			drops := strings.Join(runner.statements, "\n")
			if !strings.Contains(drops, "DROP USER IF EXISTS '"+intent.Username+"'@'127.0.0.1'") || (verb == "database.create" && !strings.Contains(drops, "DROP DATABASE IF EXISTS `targetdb`")) {
				t.Fatal("owned objects were not dropped")
			}
			if got := runner.objects["user:"+intent.Username+"@127.0.0.1"]; got != "deleting\t1\t"+intent.UserID {
				t.Fatalf("user marker: %q", got)
			}
			if verb == "database.create" {
				if got := runner.objects["schema:targetdb"]; got != "deleting\t1\t"+intent.DBID {
					t.Fatalf("schema marker: %q", got)
				}
				if _, _, e := s.DatabaseWithUsers(ctx, intent.DBID); e == nil {
					t.Fatal("partial database committed")
				}
			}
		})
	}
}
