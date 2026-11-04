package agent

import (
	"context"
	"errors"
	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestCanceledRepeatedStartupPreservesSuccessfulAdmission(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log, err := audit.Open(st, filepath.Join(t.TempDir(), "audit"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	a := &Agent{Store: st, Jobs: jobs.NewQueue(st, nil, log)}
	if err = a.PrepareStartup(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = a.PrepareStartup(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if err = a.CheckMutationAdmission(); err != nil {
		t.Fatalf("canceled no-op closed previously healthy admission: %v", err)
	}
}

func TestOverlappingCanceledStartupDoesNotLeaveAdmissionClosed(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log, err := audit.Open(st, filepath.Join(t.TempDir(), "audit"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	q := jobs.NewQueue(st, nil, log)
	a := &Agent{Store: st, Jobs: q}
	entered, release := make(chan struct{}), make(chan struct{})
	q.SetStartupRecovery(func(context.Context) (*jobs.Job, error) { close(entered); <-release; return nil, nil }, nil)
	first := make(chan error, 1)
	go func() { first <- a.PrepareStartup(context.Background()) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = a.PrepareStartup(ctx); !errors.Is(err, context.Canceled) {
		close(release)
		<-first
		t.Fatalf("cancel: %v", err)
	}
	close(release)
	select {
	case err = <-first:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("startup stuck")
	}
	if err = a.CheckMutationAdmission(); err != nil {
		t.Fatalf("completed startup left admission closed: %v", err)
	}
}
