package verbs_test

import (
	"context"
	"encoding/json"
	"errors"
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

type createReplayFault struct {
	*store.Store
	failedPublish bool
	failedRead    bool
}

func (s *createReplayFault) CommitDatabaseClaim(ctx context.Context, job, verb, key string, result json.RawMessage, nonce, ciphertext []byte) error {
	if !s.failedPublish {
		s.failedPublish = true
		return errors.New("injected publication failure")
	}
	return s.Store.CommitDatabaseClaim(ctx, job, verb, key, result, nonce, ciphertext)
}
func (s *createReplayFault) DatabaseIntentSecret(ctx context.Context, job string) ([]byte, []byte, error) {
	if s.failedPublish && !s.failedRead {
		s.failedRead = true
		return nil, nil, errors.New("injected transient input read")
	}
	return s.Store.DatabaseIntentSecret(ctx, job)
}

func TestCreateWorkerReplayAfterAppliedDDL(t *testing.T) {
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
			a.Store = s
			a.Audit = log
			a.Exec = runner
			a.Jobs = jobs.NewQueue(s, seal, log)
			server := rpc.NewServer(a)
			caller := rpc.WithCaller(ctx, rpc.Caller{Kind: "user", Role: "admin", Session: "owner", MFAat: time.Now().UnixMilli()})
			reply := server.Dispatch(caller, rpc.Request{Verb: "database.create", Args: json.RawMessage(`{"name":"testdb"}`)})
			if !reply.OK {
				t.Fatalf("database admission: %+v", reply)
			}
			en := reply.Data.(jobs.EnqueueResult)
			if verb == "dbuser.create" {
				parent, e := s.StartJob(ctx, en.Job.ID, 1)
				if e != nil {
					t.Fatal(e)
				}
				if e = server.ExecuteJob(ctx, &parent); e != nil {
					t.Fatal(e)
				}
				parent.State = "done"
				if e = s.UpdateJob(ctx, parent); e != nil {
					t.Fatal(e)
				}
				var result map[string]string
				if e = json.Unmarshal(parent.Result, &result); e != nil {
					t.Fatal(e)
				}
				args, _ := json.Marshal(map[string]string{"db_id": result["db_id"], "username": "testuser"})
				reply = server.Dispatch(caller, rpc.Request{Verb: verb, Args: args})
				if !reply.OK {
					t.Fatalf("user admission: %+v", reply)
				}
				en = reply.Data.(jobs.EnqueueResult)
			}
			raw, err := a.Jobs.OpenInput(&en.Job)
			if err != nil {
				t.Fatal(err)
			}
			var input struct {
				Password string `json:"password"`
				DBID     string `json:"db_id"`
				UserID   string `json:"user_id"`
				Username string `json:"username"`
			}
			if err = json.Unmarshal(raw, &input); err != nil {
				t.Fatal(err)
			}
			clear(raw)
			nonce, ciphertext, err := s.DatabaseIntentSecret(ctx, en.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			runner.statements = nil
			fault := &createReplayFault{Store: s}
			a.Jobs = jobs.NewQueue(fault, seal, log)
			worker, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			attempts := make(chan error, 8)
			a.Jobs.SetRunner(func(ctx context.Context, j *jobs.Job) error { e := server.ExecuteJob(ctx, j); attempts <- e; return e })
			go func() { done <- a.Jobs.Run(worker) }()
			defer func() { cancel(); <-done }()
			for i := 0; i < 2; i++ {
				select {
				case e := <-attempts:
					if !errors.Is(e, jobs.ErrRetry) {
						t.Fatalf("attempt %d: %v", i, e)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("retry timeout")
				}
			}
			select {
			case e := <-attempts:
				if e != nil {
					t.Fatalf("resume: %v", e)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("resume timeout")
			}
			if !fault.failedPublish || !fault.failedRead {
				t.Fatal("faults not exercised")
			}
			n2, c2, err := s.DatabaseIntentSecret(ctx, en.Job.ID)
			if err != nil || string(n2) != string(nonce) || string(c2) != string(ciphertext) {
				t.Fatal("sealed input changed")
			}
			claim, err := a.Jobs.Claim(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner"}), en.Job.ID, en.ClaimToken)
			if err != nil || claim["password"] != input.Password {
				t.Fatalf("claim mismatch: %v", err)
			}
			if _, err = a.Jobs.Claim(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner"}), en.Job.ID, en.ClaimToken); err == nil {
				t.Fatal("claim usable twice")
			}
			createCount := 0
			for _, stmt := range runner.statements {
				if strings.HasPrefix(stmt, "CREATE USER '") {
					createCount++
					if !strings.Contains(stmt, input.Password) {
						t.Fatal("DDL password differs from sealed input")
					}
				}
				if strings.HasPrefix(stmt, "DROP ") || strings.HasPrefix(stmt, "DELETE FROM novapanel.managed_objects") {
					t.Fatalf("unexpected destructive mutation: %s", stmt)
				}
			}
			if createCount != 1 {
				t.Fatalf("CREATE USER count %d", createCount)
			}
			if runner.objects["user:"+input.Username+"@127.0.0.1"] != "created\t1\t"+input.UserID {
				t.Fatal("user provenance not retained")
			}
		})
	}
}

func TestCreateCommittedWorkerWithoutInput(t *testing.T) {
	for _, verb := range []string{"database.create", "dbuser.create"} {
		for _, mode := range []string{"missing", "corrupt"} {
			t.Run(verb+"/"+mode, func(t *testing.T) {
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
				runner := &recordingDatabaseRunner{databaseRunner: &databaseRunner{}}
				a.Exec = runner
				q := jobs.NewQueue(s, seal, log)
				a.Jobs = q
				id := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
				uid := "01ARZ3NDEKTSV4RRFFQ69G5FAW"
				if verb == "dbuser.create" {
					parent, e := q.EnqueueDatabase(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner", Role: "admin"}), "database.create", map[string]string{"db_id": id, "user_id": "01ARZ3NDEKTSV4RRFFQ69G5FAX", "name": "testdb", "username": "parentuser"}, "db:"+id, "parentsecret", nil, nil)
					if e != nil {
						t.Fatal(e)
					}
					pj, e := s.StartJob(ctx, parent.Job.ID, 1)
					if e != nil {
						t.Fatal(e)
					}
					if e = q.PublishDatabaseClaim(ctx, &pj, json.RawMessage(`{}`), map[string]any{"password": "parentsecret"}); e != nil {
						t.Fatal(e)
					}
					pj.State = "done"
					if e = s.UpdateJob(ctx, pj); e != nil {
						t.Fatal(e)
					}
				}
				args := map[string]string{"db_id": id, "user_id": uid, "name": "testdb", "db_name": "testdb", "username": "testuser"}
				en, err := q.EnqueueDatabase(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner", Role: "admin"}), verb, args, "db:"+id, "secret", nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				j, err := s.StartJob(ctx, en.Job.ID, 1)
				if err != nil {
					t.Fatal(err)
				}
				if err = q.PublishDatabaseClaim(ctx, &j, json.RawMessage(`{"ok":true}`), map[string]any{"password": "secret"}); err != nil {
					t.Fatal(err)
				}
				j.State = "queued"
				if err = s.UpdateJob(ctx, j); err != nil {
					t.Fatal(err)
				}
				fault := &unavailableRotateInput{Store: s, mode: mode, reads: make(chan struct{}, 1)}
				a.Jobs = jobs.NewQueue(fault, seal, log)
				server := rpc.NewServer(a)
				forged := j
				forged.CreatedBy = "attacker"
				if err = server.ExecuteJob(ctx, &forged); err == nil {
					t.Fatal("forged identity accepted")
				}
				forged = j
				forged.ObjectKey = "db:other"
				if err = server.ExecuteJob(ctx, &forged); err == nil {
					t.Fatal("forged binding accepted")
				}
				worker, cancel := context.WithCancel(ctx)
				done := make(chan error, 1)
				a.Jobs.SetRunner(server.ExecuteJob)
				go func() { done <- a.Jobs.Run(worker) }()
				defer func() { cancel(); <-done }()
				deadline := time.After(5 * time.Second)
				for {
					cur, e := s.GetJob(ctx, j.ID)
					if e != nil {
						t.Fatal(e)
					}
					if cur.State == "done" {
						break
					}
					if cur.State == "failed" {
						t.Fatalf("failed: %s", cur.Code)
					}
					select {
					case <-deadline:
						t.Fatal("worker timeout")
					case <-time.After(time.Millisecond):
					}
				}
				select {
				case <-fault.reads:
					t.Fatal("committed input opened")
				default:
				}
				if len(runner.statements) != 0 {
					t.Fatal("committed replay mutated external state")
				}
			})
		}
	}
}
