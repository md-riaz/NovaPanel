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

type foreignSchemaRunner struct {
	*recordingDatabaseRunner
	foreign string
}

func (r *foreignSchemaRunner) Run(ctx context.Context, p string, args []string, opts exec.RunOpts) ([]byte, []byte, int, error) {
	b, err := io.ReadAll(opts.Stdin)
	if err != nil {
		return nil, nil, -1, err
	}
	q := string(b)
	if strings.HasPrefix(q, "SELECT COALESCE((SELECT phase FROM novapanel.managed_objects WHERE kind='schema' AND name='testdb'") {
		return []byte("none\n"), nil, 0, nil
	}
	if strings.HasPrefix(q, "SELECT COALESCE((SELECT obj_id FROM novapanel.managed_objects WHERE kind='schema' AND name='testdb'") {
		return []byte(r.foreign + "\n"), nil, 0, nil
	}
	opts.Stdin = strings.NewReader(q)
	return r.recordingDatabaseRunner.Run(ctx, p, args, opts)
}

type mismatchOwnerRunner struct{ *foreignUserRunner }

func (r *mismatchOwnerRunner) Run(ctx context.Context, p string, args []string, opts exec.RunOpts) ([]byte, []byte, int, error) {
	b, err := io.ReadAll(opts.Stdin)
	if err != nil {
		return nil, nil, -1, err
	}
	if strings.Contains(string(b), "kind='schema'") {
		opts.Stdin = strings.NewReader(string(b))
		return r.foreignUserRunner.Runner.Run(ctx, p, args, opts)
	}
	if strings.HasPrefix(string(b), "SELECT COALESCE((SELECT obj_id FROM novapanel.managed_objects WHERE kind='user' AND name='staleuser@127.0.0.1')") {
		return []byte(r.owner + "\n"), nil, 0, nil
	}
	opts.Stdin = strings.NewReader(string(b))
	return r.foreignUserRunner.Run(ctx, p, args, opts)
}

func TestQueueDeleteForeignSchemaWithLiveChildren(t *testing.T) {
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
	const db = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	const child = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	const foreign = "01ARZ3NDEKTSV4RRFFQ69G5FAX"
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO databases(id,name,engine,created) VALUES(?,'testdb','mariadb',0)`, db); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO db_users(id,db_id,username,host,owner,created) VALUES(?,?,'liveuser','127.0.0.1',0,0)`, child, db)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	external := &databaseRunner{objects: map[string]string{"schema:testdb": "created\t1\t" + foreign, "user:liveuser@127.0.0.1": "created\t1\t" + child}}
	r := &foreignSchemaRunner{recordingDatabaseRunner: &recordingDatabaseRunner{databaseRunner: external}, foreign: foreign}
	a := agent.New()
	a.Store, a.Audit, a.Exec = s, log, r
	a.Jobs = jobs.NewQueue(s, seal, log)
	server := rpc.NewServer(a)
	a.Jobs.SetRunner(server.ExecuteJob)
	raw, _ := json.Marshal(map[string]string{"db_id": db})
	reply := server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Kind: "user", Role: "admin", Session: "owner", MFAat: time.Now().UnixMilli()}), rpc.Request{Verb: "database.delete", Args: raw})
	if !reply.OK {
		t.Fatalf("admission: %+v", reply)
	}
	jobID := reply.Data.(jobs.EnqueueResult).Job.ID
	wc, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(wc) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		j, err := s.GetJob(ctx, jobID)
		if err != nil {
			t.Fatal(err)
		}
		if j.State == "failed" || j.State == "done" {
			if j.State != "failed" || j.Code != "conflict" {
				t.Fatalf("terminal job: %+v", j)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not terminate: %+v", j)
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, users, err := s.DatabaseWithUsers(ctx, db)
	if err != nil || len(users) != 1 || users[0].ID != child {
		t.Fatalf("live children lost: %+v %v", users, err)
	}
	if external.objects["user:liveuser@127.0.0.1"] != "created\t1\t"+child || external.objects["schema:testdb"] != "created\t1\t"+foreign {
		t.Fatalf("external state changed: %+v", external.objects)
	}
	for _, q := range r.statements {
		if strings.HasPrefix(q, "DROP ") {
			t.Fatalf("unexpected DROP: %s", q)
		}
	}
}

func TestQueueDeleteForeignUserMarker(t *testing.T) {
	for _, verb := range []string{"database.delete", "dbuser.delete"} {
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
			const db = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
			const stale = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
			const sibling = "01ARZ3NDEKTSV4RRFFQ69G5FAX"
			const foreign = "01ARZ3NDEKTSV4RRFFQ69G5FAY"
			err = s.Tx(ctx, func(tx *sql.Tx) error {
				if _, err := tx.Exec(`INSERT INTO databases(id,name,engine,created) VALUES(?,'testdb','mariadb',0)`, db); err != nil {
					return err
				}
				_, err := tx.Exec(`INSERT INTO db_users(id,db_id,username,host,owner,created) VALUES(?,?,'staleuser','127.0.0.1',0,0),(?,?,'siblinguser','127.0.0.1',0,1)`, stale, db, sibling, db)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			external := &databaseRunner{objects: map[string]string{"schema:testdb": "created\t1\t" + db, "user:siblinguser@127.0.0.1": "created\t1\t" + sibling}}
			runner := &foreignUserRunner{Runner: external, username: "staleuser", userID: stale, owner: foreign, phase: "created", present: true}
			a := agent.New()
			a.Store, a.Audit, a.Exec = s, log, &mismatchOwnerRunner{runner}
			a.Jobs = jobs.NewQueue(s, seal, log)
			server := rpc.NewServer(a)
			a.Jobs.SetRunner(server.ExecuteJob)
			args := map[string]string{"dbuser_id": stale}
			if verb == "database.delete" {
				args = map[string]string{"db_id": db}
			}
			raw, err := json.Marshal(args)
			if err != nil {
				t.Fatal(err)
			}
			reply := server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Kind: "user", Role: "admin", Session: "owner", MFAat: time.Now().UnixMilli()}), rpc.Request{Verb: verb, Args: raw})
			if !reply.OK {
				t.Fatalf("admission: %+v", reply)
			}
			jobID := reply.Data.(jobs.EnqueueResult).Job.ID
			wc, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- a.Jobs.Run(wc) }()
			defer func() {
				cancel()
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			deadline := time.Now().Add(5 * time.Second)
			for {
				j, err := s.GetJob(ctx, jobID)
				if err != nil {
					t.Fatal(err)
				}
				if j.State == "failed" || j.State == "done" {
					if j.State != "failed" || j.Code != "conflict" {
						t.Fatalf("terminal job: %+v", j)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("job did not terminate: %+v", j)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !runner.present || runner.owner != foreign || runner.phase != "created" {
				t.Fatalf("foreign object changed: %+v", runner)
			}
			if _, err := s.DatabaseUserByID(ctx, stale); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("stale owner retained: %v", err)
			}
			parent, users, err := s.DatabaseWithUsers(ctx, db)
			if err != nil || parent.Name != "testdb" || len(users) != 1 || users[0].ID != sibling {
				t.Fatalf("parent or sibling changed: %+v %+v %v", parent, users, err)
			}
			if external.objects["schema:testdb"] != "created\t1\t"+db || external.objects["user:siblinguser@127.0.0.1"] != "created\t1\t"+sibling {
				t.Fatalf("parent or sibling external state changed: %+v", external.objects)
			}
		})
	}
}
