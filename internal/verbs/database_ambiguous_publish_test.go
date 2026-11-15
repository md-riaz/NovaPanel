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

type ambiguousCreateStore struct {
	*store.Store
	injected bool
}

func (s *ambiguousCreateStore) CommitDatabaseClaim(ctx context.Context, job, verb, key string, result json.RawMessage, nonce, ciphertext []byte) error {
	err := s.Store.CommitDatabaseClaim(ctx, job, verb, key, result, nonce, ciphertext)
	if err == nil && !s.injected {
		s.injected = true
		return errors.New("lost commit acknowledgement")
	}
	return err
}
func TestCreateAmbiguousPublicationQueue(t *testing.T) {
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
	wrapped := &ambiguousCreateStore{Store: s}
	a.Jobs = jobs.NewQueue(wrapped, seal, log)
	server := rpc.NewServer(a)
	caller := rpc.WithCaller(ctx, rpc.Caller{Kind: "user", Role: "admin", Session: "owner", MFAat: time.Now().UnixMilli()})
	reply := server.Dispatch(caller, rpc.Request{Verb: "database.create", Args: json.RawMessage(`{"name":"ambiguousdb"}`)})
	if !reply.OK {
		t.Fatalf("admission: %+v", reply)
	}
	en := reply.Data.(jobs.EnqueueResult)
	attempts := make(chan error, 4)
	a.Jobs.SetRunner(func(ctx context.Context, j *jobs.Job) error { e := server.ExecuteJob(ctx, j); attempts <- e; return e })
	worker, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(worker) }()
	defer func() { cancel(); <-done }()
	for i := 0; i < 2; i++ {
		select {
		case e := <-attempts:
			if i == 0 && !errors.Is(e, jobs.ErrRetry) {
				t.Fatalf("first attempt: %v", e)
			}
			if i == 1 && e != nil {
				t.Fatalf("replay: %v", e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timeout")
		}
	}
	if !wrapped.injected {
		t.Fatal("fault not exercised")
	}
	for _, stmt := range runner.statements {
		if strings.HasPrefix(stmt, "DROP ") {
			t.Fatalf("ambiguous commit caused cleanup: %s", stmt)
		}
	}
	if _, err = a.Jobs.Claim(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner"}), en.Job.ID, en.ClaimToken); err != nil {
		t.Fatalf("committed claim lost: %v", err)
	}
}
