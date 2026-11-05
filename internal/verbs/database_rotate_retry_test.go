package verbs_test

import (
	"context"
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

type indeterminateRotateRunner struct {
	*recordingDatabaseRunner
	failed          bool
	preflightFailed bool
	applied         string
}

func (r *indeterminateRotateRunner) Run(ctx context.Context, program string, args []string, opts exec.RunOpts) ([]byte, []byte, int, error) {
	b, err := io.ReadAll(opts.Stdin)
	if err != nil {
		return nil, nil, -1, err
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

func TestRotatePublishFailureWorkerReplaysSealedPassword(t *testing.T) {
	testRotateWorkerReplay(t, false)
}

func TestRotateIndeterminateAlterWorkerReplaysSealedPassword(t *testing.T) {
	testRotateWorkerReplay(t, true)
}

func testRotateWorkerReplay(t *testing.T, failAlter bool) {
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
	if failAlter {
		indeterminate = &indeterminateRotateRunner{recordingDatabaseRunner: runner}
		a.Exec = indeterminate
		a.Jobs = jobs.NewQueue(s, seal, log)
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
	if alters != 2 {
		t.Fatalf("expected two rotations, got %d", alters)
	}
}
