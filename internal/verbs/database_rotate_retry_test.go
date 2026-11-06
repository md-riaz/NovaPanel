package verbs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
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

type failRotatePublish struct {
	*store.Store
	mu     sync.Mutex
	failed bool
}

func (s *failRotatePublish) CommitDatabaseClaim(ctx context.Context, job, verb, key string, result json.RawMessage, nonce, ciphertext []byte) error {
	s.mu.Lock()
	if verb == "database.rotate_password" && !s.failed {
		s.failed = true
		s.mu.Unlock()
		return errors.New("injected publication failure")
	}
	s.mu.Unlock()
	return s.Store.CommitDatabaseClaim(ctx, job, verb, key, result, nonce, ciphertext)
}

type failRotateInputRead struct {
	*store.Store
	runner *indeterminateRotateRunner
	failed bool
}

func (s *failRotateInputRead) DatabaseIntentSecret(ctx context.Context, jobID string) ([]byte, []byte, error) {
	if s.runner.failed && !s.failed {
		s.failed = true
		return nil, nil, errors.New("injected SQLite read failure")
	}
	return s.Store.DatabaseIntentSecret(ctx, jobID)
}

type unavailableRotateInput struct {
	*store.Store
	mode                            string
	foreignNonce, foreignCiphertext []byte
	reads                           chan struct{}
}

func (s *unavailableRotateInput) DatabaseIntentSecret(ctx context.Context, jobID string) ([]byte, []byte, error) {
	if s.reads != nil {
		select {
		case s.reads <- struct{}{}:
		default:
		}
	}
	switch s.mode {
	case "missing":
		return nil, nil, sql.ErrNoRows
	case "corrupt":
		return []byte("corrupt"), []byte("corrupt"), nil
	case "foreign":
		return s.foreignNonce, s.foreignCiphertext, nil
	default:
		return nil, nil, errors.New("injected transient read failure")
	}
}

type secondRotateReadFails struct {
	*store.Store
	reads int
}

func (s *secondRotateReadFails) DatabaseIntentSecret(ctx context.Context, jobID string) ([]byte, []byte, error) {
	s.reads++
	if s.reads == 2 {
		return nil, nil, errors.New("transient second read")
	}
	return s.Store.DatabaseIntentSecret(ctx, jobID)
}

type loseRotateConflictReply struct{ *store.Store }

func (s *loseRotateConflictReply) CommitRotateConflictClaim(ctx context.Context, job, verb, key string, result json.RawMessage, nonce, ciphertext []byte, code string) error {
	if err := s.Store.CommitRotateConflictClaim(ctx, job, verb, key, result, nonce, ciphertext, code); err != nil {
		return err
	}
	return errors.New("injected lost publication reply")
}

type indeterminateRotateRunner struct {
	*recordingDatabaseRunner
	failed          bool
	preflightFailed bool
	applied         string
	conflict        bool
}

func (r *indeterminateRotateRunner) Run(ctx context.Context, program string, args []string, opts exec.RunOpts) ([]byte, []byte, int, error) {
	b, err := io.ReadAll(opts.Stdin)
	if err != nil {
		return nil, nil, -1, err
	}
	if r.failed && r.conflict && strings.Contains(string(b), "SELECT COALESCE((SELECT phase") {
		return []byte("created\t1\tforeign\n"), nil, 0, nil
	}
	if r.failed && !r.preflightFailed && strings.Contains(string(b), "SELECT COALESCE((SELECT phase") {
		r.preflightFailed = true
		return nil, nil, -1, errors.New("injected ownership read failure")
	}
	opts.Stdin = strings.NewReader(string(b))
	out, stderr, code, err := r.recordingDatabaseRunner.Run(ctx, program, args, opts)
	if strings.HasPrefix(string(b), "ALTER USER") {
		r.applied = string(b) // Model the server committing the password.
		if !r.failed {
			r.failed = true
			return nil, nil, -1, errors.New("injected client failure")
		}
	}
	return out, stderr, code, err
}

func TestRotateUnreadableInputRetainsStartedJob(t *testing.T) {
	for _, mode := range []string{"missing", "corrupt", "foreign", "transient"} {
		t.Run(mode, func(t *testing.T) {
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
			q := jobs.NewQueue(s, seal, log)
			meta := jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner", Role: "admin", Automation: true})
			args := map[string]string{"db_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV"}
			rotation, err := q.EnqueueDatabase(meta, "database.rotate_password", args, "db:01ARZ3NDEKTSV4RRFFQ69G5FAV", "secret", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			other, err := q.EnqueueDatabase(meta, "database.rotate_password", args, "db:01ARZ3NDEKTSV4RRFFQ69G5FAV", "foreign", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			nonce, ciphertext, err := s.DatabaseIntentSecret(ctx, rotation.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			fn, fc, err := s.DatabaseIntentSecret(ctx, other.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			fault := &unavailableRotateInput{Store: s, mode: mode, foreignNonce: fn, foreignCiphertext: fc, reads: make(chan struct{}, 1)}
			a.Jobs = jobs.NewQueue(fault, seal, log)
			server := rpc.NewServer(a)
			e := server.ExecuteJob(ctx, &rotation.Job)
			if mode == "transient" {
				if !errors.Is(e, jobs.ErrRetry) {
					t.Fatalf("precheckpoint transient error: %v", e)
				}
			} else if e == nil || e.Error() != "Invalid sealed arguments" {
				t.Fatalf("precheckpoint error: %v", e)
			}
			j, err := s.GetJob(ctx, rotation.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			j.Step = "rotation_started"
			if err = s.UpdateJob(ctx, j); err != nil {
				t.Fatal(err)
			}
			if e := server.ExecuteJob(ctx, &j); !errors.Is(e, jobs.ErrRetry) {
				t.Fatalf("started error: %v", e)
			}
			if mode == "transient" {
				j.Step = ""
				if err = s.UpdateJob(ctx, j); err != nil {
					t.Fatal(err)
				}
			}
			// Remove the unrelated queued job from eligibility without touching the input under test.
			otherJob, err := s.GetJob(ctx, other.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			otherJob.State = "done"
			if err = s.UpdateJob(ctx, otherJob); err != nil {
				t.Fatal(err)
			}
			select {
			case <-fault.reads:
			default:
			}
			a.Jobs.SetRunner(server.ExecuteJob)
			workerCtx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- a.Jobs.Run(workerCtx) }()
			select {
			case <-fault.reads:
			case <-time.After(5 * time.Second):
				cancel()
				<-done
				t.Fatal("worker did not attempt input")
			}
			deadline := time.After(5 * time.Second)
			for {
				j, err = s.GetJob(ctx, j.ID)
				if err != nil {
					t.Fatal(err)
				}
				if j.State == "queued" {
					break
				}
				if j.State == "failed" {
					t.Fatalf("terminalized: %s", j.Code)
				}
				select {
				case <-deadline:
					t.Fatal("worker did not requeue")
				case <-time.After(time.Millisecond):
				}
			}
			cancel()
			<-done
			n2, c2, err := s.DatabaseIntentSecret(ctx, j.ID)
			if err != nil || string(n2) != string(nonce) || string(c2) != string(ciphertext) {
				t.Fatal("sealed input changed")
			}
			if len(runner.statements) != 0 {
				t.Fatal("external mutation attempted")
			}
		})
	}
}

func TestRotatePublishFailureWorkerReplaysSealedPassword(t *testing.T) {
	testRotateWorkerReplay(t, false)
}

func TestRotateIndeterminateAlterWorkerReplaysSealedPassword(t *testing.T) {
	testRotateWorkerReplay(t, true)
}

func TestRotateInputReadFailureAfterIndeterminateAlter(t *testing.T) {
	testRotateWorker(t, true, false, true)
}

func TestRotateConflictAfterIndeterminateAlterPreservesClaim(t *testing.T) {
	testRotateWorker(t, true, true, false)
}

func testRotateWorkerReplay(t *testing.T, failAlter bool) {
	testRotateWorker(t, failAlter, false, false)
}

func testRotateWorker(t *testing.T, failAlter, conflict, failInput bool) {
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
	a.Jobs = jobs.NewQueue(s, seal, log)
	server := rpc.NewServer(a)
	caller := rpc.Caller{Kind: "user", Role: "admin", Session: "owner", MFAat: time.Now().UnixMilli()}
	reply := server.Dispatch(rpc.WithCaller(ctx, caller), rpc.Request{Verb: "database.create", Args: json.RawMessage(`{}`)})
	if !reply.OK {
		t.Fatalf("create admission: %+v", reply)
	}
	created := reply.Data.(jobs.EnqueueResult)
	j, err := s.StartJob(ctx, created.Job.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = server.ExecuteJob(ctx, &j); err != nil {
		t.Fatal(err)
	}
	j.State = "done"
	if err = s.UpdateJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	var result map[string]string
	if err = json.Unmarshal(j.Result, &result); err != nil {
		t.Fatal(err)
	}
	var indeterminate *indeterminateRotateRunner
	var failingInput *failRotateInputRead
	if failAlter {
		indeterminate = &indeterminateRotateRunner{recordingDatabaseRunner: runner, conflict: conflict}
		a.Exec = indeterminate
		if conflict {
			a.Jobs = jobs.NewQueue(&loseRotateConflictReply{Store: s}, seal, log)
		} else if failInput {
			failingInput = &failRotateInputRead{Store: s, runner: indeterminate}
			a.Jobs = jobs.NewQueue(failingInput, seal, log)
		} else {
			a.Jobs = jobs.NewQueue(s, seal, log)
		}
	} else {
		a.Jobs = jobs.NewQueue(&failRotatePublish{Store: s}, seal, log)
	}
	a.Jobs.SetRunner(server.ExecuteJob)
	args, _ := json.Marshal(map[string]string{"db_id": result["db_id"]})
	reply = server.Dispatch(rpc.WithCaller(ctx, caller), rpc.Request{Verb: "database.rotate_password", Args: args})
	if !reply.OK {
		t.Fatalf("rotate admission: %+v", reply)
	}
	rotation := reply.Data.(jobs.EnqueueResult)
	raw, err := a.Jobs.OpenInput(&rotation.Job)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	var input struct {
		Password string `json:"password"`
	}
	if err = json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	runner.statements = nil
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(workerCtx) }()
	defer func() { cancel(); <-done }()
	deadline := time.After(5 * time.Second)
	for {
		current, e := s.GetJob(ctx, rotation.Job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if current.State == "failed" {
			if conflict && current.Code == "conflict" {
				break
			}
			t.Fatalf("rotation terminalized after publication failure: %s", current.Code)
		}
		if current.State == "done" {
			break
		}
		select {
		case <-deadline:
			t.Fatal("rotation did not complete")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if failInput && !failingInput.failed {
		t.Fatal("SQLite read failure was not exercised")
	}
	if failAlter && (!indeterminate.failed || !strings.Contains(indeterminate.applied, input.Password)) {
		t.Fatal("server did not apply sealed password")
	}
	claim, err := a.Jobs.Claim(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner"}), rotation.Job.ID, rotation.ClaimToken)
	if err != nil || claim["password"] != input.Password {
		t.Fatalf("claim did not preserve sealed password: %v", err)
	}
	if _, err = a.Jobs.Claim(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner"}), rotation.Job.ID, rotation.ClaimToken); err == nil {
		t.Fatal("claim succeeded twice")
	}
	alters := 0
	for _, statement := range runner.statements {
		if strings.HasPrefix(statement, "ALTER USER") {
			alters++
			if !strings.Contains(statement, input.Password) {
				t.Fatal("replay changed password")
			}
		}
	}
	want := 2
	if conflict {
		want = 1
	}
	if alters != want {
		t.Fatalf("expected %d rotations, got %d", want, alters)
	}
}

func TestRotateCommittedWorkerWithoutInput(t *testing.T) {
	for _, code := range []string{"", "conflict"} {
		for _, mode := range []string{"missing", "corrupt"} {
			t.Run(code+"/"+mode, func(t *testing.T) {
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
				meta := jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner", Role: "admin", Automation: true})
				id := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
				result, err := q.EnqueueDatabase(meta, "database.rotate_password", map[string]string{"db_id": id}, "db:"+id, "secret", nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				j, err := s.StartJob(ctx, result.Job.ID, 1)
				if err != nil {
					t.Fatal(err)
				}
				if code == "" {
					err = q.PublishDatabaseClaim(ctx, &j, json.RawMessage(`{}`), map[string]any{"password": "secret"})
				} else {
					err = q.PublishRotateConflictClaim(ctx, &j, map[string]any{"password": "secret"}, code)
				}
				if err != nil {
					t.Fatal(err)
				}
				j.State = "queued"
				if err = s.UpdateJob(ctx, j); err != nil {
					t.Fatal(err)
				}
				fault := &unavailableRotateInput{Store: s, mode: mode, reads: make(chan struct{}, 1)}
				a.Jobs = jobs.NewQueue(fault, seal, log)
				server := rpc.NewServer(a)
				a.Jobs.SetRunner(server.ExecuteJob)
				forged := j
				forged.CreatedBy = "attacker"
				if err = server.ExecuteJob(ctx, &forged); err == nil {
					t.Fatal("forged owner accepted")
				}
				forged = j
				forged.ObjectKey = "db:other"
				if err = server.ExecuteJob(ctx, &forged); err == nil {
					t.Fatal("forged object accepted")
				}
				worker, cancel := context.WithCancel(ctx)
				done := make(chan error, 1)
				go func() { done <- a.Jobs.Run(worker) }()
				deadline := time.After(5 * time.Second)
				for {
					current, e := s.GetJob(ctx, j.ID)
					if e != nil {
						t.Fatal(e)
					}
					if current.State == "done" || current.State == "failed" {
						if code == "" && current.State != "done" || code != "" && (current.State != "failed" || current.Code != code) {
							t.Fatalf("unexpected outcome %+v", current)
						}
						break
					}
					select {
					case <-deadline:
						cancel()
						<-done
						t.Fatal("worker timeout")
					case <-time.After(time.Millisecond):
					}
				}
				cancel()
				<-done
				select {
				case <-fault.reads:
					t.Fatal("committed replay opened input")
				default:
				}
				if len(runner.statements) != 0 {
					t.Fatal("external execution")
				}
			})
		}
	}
}

func TestRotateSecondInputReadTransient(t *testing.T) {
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
	fault := &secondRotateReadFails{Store: s}
	a.Jobs = jobs.NewQueue(fault, seal, log)
	id := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	meta := jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner", Role: "admin", Automation: true})
	result, err := a.Jobs.EnqueueDatabase(meta, "database.rotate_password", map[string]string{"db_id": id, "user_id": id, "name": "db", "username": "user"}, "db:"+id, "secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer(a)
	worker, cancel := context.WithCancel(ctx)
	attempt := make(chan error, 1)
	a.Jobs.SetRunner(func(ctx context.Context, j *jobs.Job) error {
		err := server.ExecuteJob(ctx, j)
		attempt <- err
		cancel()
		return err
	})
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(worker) }()
	select {
	case err = <-attempt:
	case <-time.After(5 * time.Second):
		cancel()
		<-done
		t.Fatal("worker timeout")
	}
	<-done
	if !errors.Is(err, jobs.ErrRetry) || fault.reads != 2 {
		t.Fatalf("second read: %v reads=%d", err, fault.reads)
	}
	current, err := s.GetJob(ctx, result.Job.ID)
	if err != nil || current.State == "failed" || current.State == "done" {
		t.Fatalf("transient read terminalized: %+v %v", current, err)
	}
}
