package verbs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

func TestDeletePersistentConflictTerminalizes(t *testing.T) {
	for _, verb := range []string{"dbuser.delete", "database.delete"} {
		t.Run(verb, func(t *testing.T) {
			ctx := context.Background()
			s, e := store.Open(":memory:")
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			log, e := audit.Open(s, filepath.Join(t.TempDir(), "audit"))
			if e != nil {
				t.Fatal(e)
			}
			defer log.Close()
			seal, e := secret.New(make([]byte, 32))
			if e != nil {
				t.Fatal(e)
			}
			const db = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
			const user = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
			e = s.Tx(ctx, func(tx *sql.Tx) error {
				if _, e := tx.Exec(`INSERT INTO databases(id,name,engine,created) VALUES(?,'testdb','mariadb',0)`, db); e != nil {
					return e
				}
				_, e := tx.Exec(`INSERT INTO db_users(id,db_id,username,host,owner,created) VALUES(?,?,'testuser','127.0.0.1',0,0)`, user, db)
				return e
			})
			if e != nil {
				t.Fatal(e)
			}
			r := &recordingDatabaseRunner{databaseRunner: &databaseRunner{objects: map[string]string{"schema:testdb": "created\t1\t" + db, "user:testuser@127.0.0.1": "reserved\t1\t" + user}}}
			a := agent.New()
			a.Store = s
			a.Audit = log
			a.Exec = r
			a.Jobs = jobs.NewQueue(s, seal, log)
			server := rpc.NewServer(a)
			args := map[string]string{"dbuser_id": user}
			if verb == "database.delete" {
				args = map[string]string{"db_id": db}
			}
			raw, _ := json.Marshal(args)
			reply := server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Kind: "user", Role: "admin", Session: "owner", MFAat: time.Now().UnixMilli()}), rpc.Request{Verb: verb, Args: raw})
			if !reply.OK {
				t.Fatal(reply)
			}
			job := reply.Data.(jobs.EnqueueResult).Job
			a.Jobs.SetRunner(server.ExecuteJob)
			wc, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- a.Jobs.Run(wc) }()
			defer func() { cancel(); <-done }()
			deadline := time.Now().Add(5 * time.Second)
			for {
				j, e := s.GetJob(ctx, job.ID)
				if e != nil {
					t.Fatal(e)
				}
				if j.State == "failed" {
					if j.Code != "conflict" {
						t.Fatalf("job: %+v", j)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("job did not fail: %+v", j)
				}
				time.Sleep(10 * time.Millisecond)
			}
			for _, q := range r.statements {
				if len(q) >= 5 && q[:5] == "DROP " {
					t.Fatalf("uncertain object dropped: %s", q)
				}
			}
		})
	}
}
