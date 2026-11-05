package verbs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
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

// Run only in the disposable MariaDB fixture, never against a host database.
func TestLiveDatabaseDeadlineRequeuesSameClaim(t *testing.T) {
	if os.Getenv("NOVAPANEL_ISOLATION_TEST") != "1" {
		t.Skip("requires disposable MariaDB fixture")
	}
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
	caller := rpc.Caller{Kind: "user", Role: "admin", Session: "owner", MFAat: time.Now().UnixMilli()}
	reply := server.Dispatch(rpc.WithCaller(ctx, caller), rpc.Request{Verb: "database.create", Args: json.RawMessage(`{"name":"deadline_fixture"}`)})
	if !reply.OK {
		t.Fatalf("enqueue: %+v", reply)
	}
	en := reply.Data.(jobs.EnqueueResult)
	nonce, cipher, err := s.DatabaseIntentSecret(ctx, en.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := seal.Open(secret.Sealed{Nonce: nonce, Ciphertext: cipher})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(plain)
	var original struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(plain, &original); err != nil || original.Password == "" {
		t.Fatal("invalid sealed intent")
	}
	var attempts atomic.Int32
	second := make(chan struct{})
	release := make(chan struct{})
	server.Agent.Jobs.SetAttemptDeadline(func(string) time.Duration {
		if attempts.Load() == 0 {
			return 50 * time.Millisecond
		}
		return 30 * time.Second
	})
	server.Agent.Jobs.SetRunner(func(c context.Context, j *jobs.Job) error {
		if attempts.Add(1) == 1 {
			<-c.Done()
			return c.Err()
		}
		close(second)
		select {
		case <-release:
			err := server.ExecuteJob(c, j)
			if err != nil {
				t.Errorf("live executor: %v", err)
			}
			return err
		case <-c.Done():
			return c.Err()
		}
	})
	wc, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(wc) }()
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	select {
	case <-second:
	case <-time.After(5 * time.Second):
		t.Fatal("no retry")
	}
	j, err := s.GetJob(ctx, en.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	n, c, err := s.DatabaseIntentSecret(ctx, en.Job.ID)
	if err != nil || !bytes.Equal(n, nonce) || !bytes.Equal(c, cipher) || !bytes.Equal(j.Args, en.Job.Args) || j.Attempts != 2 {
		t.Fatalf("retry intent changed: %+v %v", j, err)
	}
	close(release)
	limit := time.Now().Add(30 * time.Second)
	for {
		j, err = s.GetJob(ctx, en.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if j.State == "done" || j.State == "failed" {
			break
		}
		if time.Now().After(limit) {
			t.Fatal("worker did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if j.State != "done" {
		t.Fatalf("live DB job: %+v", j)
	}
	claim, err := a.Jobs.Claim(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner"}), en.Job.ID, en.ClaimToken)
	if err != nil {
		t.Fatal(err)
	}
	if claim["password"] != original.Password {
		t.Fatal("claim differs from original sealed password")
	}
	if _, err = a.Jobs.Claim(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner"}), en.Job.ID, en.ClaimToken); err == nil {
		t.Fatal("claim returned twice")
	}
}
