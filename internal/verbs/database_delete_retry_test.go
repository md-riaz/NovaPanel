package verbs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

type deleteDropFault struct {
	*databaseRunner
	hit bool
}

func (r *deleteDropFault) Run(ctx context.Context, p string, a []string, o exec.RunOpts) ([]byte, []byte, int, error) {
	b, e := io.ReadAll(o.Stdin)
	if e != nil {
		return nil, nil, -1, e
	}
	o.Stdin = strings.NewReader(string(b))
	if !r.hit && strings.HasPrefix(string(b), "DROP ") {
		r.hit = true
		return nil, nil, 1, errors.New("injected SQL failure")
	}
	return r.databaseRunner.Run(ctx, p, a, o)
}

func TestDeleteWorkerRetriesAfterDeletingMarker(t *testing.T) {
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
				if _, e := tx.Exec(`INSERT INTO databases(id,name,engine,created) VALUES(?, 'testdb','mariadb',0)`, db); e != nil {
					return e
				}
				_, e := tx.Exec(`INSERT INTO db_users(id,db_id,username,host,owner,created) VALUES(?,?,'testuser','127.0.0.1',0,0)`, user, db)
				return e
			})
			if e != nil {
				t.Fatal(e)
			}
			r := &deleteDropFault{databaseRunner: &databaseRunner{objects: map[string]string{"schema:testdb": "created\t1\t" + db, "user:testuser@127.0.0.1": "created\t1\t" + user}}}
			a := agent.New()
			a.Store = s
			a.Audit = log
			a.Exec = r
			a.Jobs = jobs.NewQueue(s, seal, log)
			server := rpc.NewServer(a)
			arg := map[string]string{"dbuser_id": user}
			if verb == "database.delete" {
				arg = map[string]string{"db_id": db}
			}
			raw, _ := json.Marshal(arg)
			reply := server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Kind: "user", Role: "admin", Session: "owner", MFAat: time.Now().UnixMilli()}), rpc.Request{Verb: verb, Args: raw})
			if !reply.OK {
				t.Fatalf("admission: %+v", reply)
			}
			job := reply.Data.(jobs.EnqueueResult).Job
			attempts := make(chan error, 4)
			a.Jobs.SetRunner(func(c context.Context, j *jobs.Job) error { e := server.ExecuteJob(c, j); attempts <- e; return e })
			wc, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- a.Jobs.Run(wc) }()
			defer func() { cancel(); <-done }()
			select {
			case err := <-attempts:
				if !errors.Is(err, jobs.ErrRetry) {
					t.Fatalf("after deleting marker must retry, got %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no attempt")
			}
			select {
			case err := <-attempts:
				if err != nil {
					t.Fatalf("replay: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no replay")
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				j, e := s.GetJob(ctx, job.ID)
				if e != nil {
					t.Fatal(e)
				}
				if j.State == "done" {
					break
				}
				if j.State == "failed" || time.Now().After(deadline) {
					t.Fatalf("job: %+v", j)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !r.hit {
				t.Fatal("fault not reached")
			}
			if verb == "dbuser.delete" {
				_, e = s.DatabaseUserByID(ctx, user)
			} else {
				_, _, e = s.DatabaseWithUsers(ctx, db)
			}
			if !errors.Is(e, sql.ErrNoRows) {
				t.Fatalf("owner survives: %v", e)
			}
		})
	}
}
