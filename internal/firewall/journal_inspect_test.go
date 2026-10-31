package firewall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJournalInspect(t *testing.T) {
	j, r := journalFixture(t)
	ctx := context.Background()
	if _, present, e := j.Inspect(ctx); e != nil || present {
		t.Fatalf("absent: %v %v", present, e)
	}
	if e := j.Arm(ctx, r); e != nil {
		t.Fatal(e)
	}
	before, e := os.Stat(filepath.Join(j.root, "state"))
	if e != nil {
		t.Fatal(e)
	}
	got, present, e := j.Inspect(ctx)
	if e != nil || !present || got != r {
		t.Fatalf("%+v %v %v", got, present, e)
	}
	got.State = "confirmed"
	again, _, e := j.Inspect(ctx)
	if e != nil || again != r {
		t.Fatal(again, e)
	}
	after, e := os.Stat(filepath.Join(j.root, "state"))
	if e != nil || !os.SameFile(before, after) {
		t.Fatal("inspection replaced state", e)
	}
	if e = os.Remove(filepath.Join(j.root, "lock")); e != nil {
		t.Fatal(e)
	}
	if _, _, e = j.Inspect(ctx); e == nil {
		t.Fatal("missing lock treated as absence")
	}
}

func TestJournalInspectLockCancellation(t *testing.T) {
	j, _ := journalFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- j.locked(context.Background(), func() error { close(entered); <-release; return nil }) }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, e := j.Inspect(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Error(e)
	}
	if _, e := OpenJournalContext(ctx, j.root); !errors.Is(e, context.DeadlineExceeded) {
		t.Error(e)
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
