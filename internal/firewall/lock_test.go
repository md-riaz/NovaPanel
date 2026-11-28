package firewall

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestLockSerializesAndCancels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "firewall.lock")
	release, err := Lock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err = Lock(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock: %v", err)
	}
	if err = release(); err != nil {
		t.Fatal(err)
	}
	release, err = Lock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err = release(); err != nil {
		t.Fatal(err)
	}
}
