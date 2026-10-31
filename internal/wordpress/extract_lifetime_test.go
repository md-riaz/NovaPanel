package wordpress

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStageFailureClassification(t *testing.T) {
	p, m := stageFixture(t)
	root := filepath.Join(t.TempDir(), "custody")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	b := StageBinding{JobID: "original-job", SiteID: "original-site", Docroot: "/srv/sites/example/public", UID: 23451, GID: 23452}
	err := extractStaged(context.Background(), p, &stagedRunner{t: t, status: validStatus(m)}, root, b, m, uint32(os.Getuid()), true, func(*os.File) error { return errors.New("sync failure") })
	if !errors.Is(err, errStaged) || errors.Is(err, context.Canceled) {
		t.Fatalf("uncanceled sync failure = %v", err)
	}
}

func TestConcurrentFreshStageAllSucceed(t *testing.T) {
	p, m := stageFixture(t)
	root := filepath.Join(t.TempDir(), "custody")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	b := StageBinding{JobID: "original-job", SiteID: "original-site", Docroot: "/srv/sites/example/public", UID: 23451, GID: 23452}
	const n = 16
	start, results := make(chan struct{}), make(chan error, n)
	for range n {
		go func() {
			<-start
			results <- extractStaged(context.Background(), p, &stagedRunner{t: t, status: validStatus(m)}, root, b, m, uint32(os.Getuid()), true, (*os.File).Sync)
		}()
	}
	close(start)
	for range n {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
}

func TestConcurrentFreshStageSerializesWrites(t *testing.T) {
	p, m := stageFixture(t)
	root := filepath.Join(t.TempDir(), "custody")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	b := StageBinding{JobID: "original-job", SiteID: "original-site", Docroot: "/srv/sites/example/public", UID: 23451, GID: 23452}
	entered, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		held := false
		first <- extractStaged(context.Background(), p, &stagedRunner{t: t, status: validStatus(m)}, root, b, m, uint32(os.Getuid()), true, func(f *os.File) error {
			if !held {
				held = true
				close(entered)
				<-release
			}
			return f.Sync()
		})
	}()
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer func() {
		unblock()
		if err := <-first; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first writer did not enter")
	}
	const n = 16
	results := make(chan error, n)
	contexts := make([]context.CancelFunc, n)
	for i := range contexts {
		ctx, cancel := context.WithCancel(context.Background())
		contexts[i] = cancel
		defer cancel()
		go func() {
			results <- extractStaged(ctx, p, &stagedRunner{t: t, status: validStatus(m)}, root, b, m, uint32(os.Getuid()), true, (*os.File).Sync)
		}()
	}
	// All calls must wait instead of comparing the first writer's unpublished
	// identity file. Cancellation must release waiters without releasing its lock.
	select {
	case err := <-results:
		t.Fatalf("overlapping writer returned before owner drained: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	for _, cancel := range contexts {
		cancel()
	}
	for range contexts {
		select {
		case err := <-results:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("wait cancellation = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("stage waiter did not cancel")
		}
	}
}
