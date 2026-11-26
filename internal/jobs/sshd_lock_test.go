package jobs

import (
	"context"
	"errors"
	"testing"
)

func TestLockSSHDWorkerYields(t *testing.T) {
	q := &Queue{}
	release, err := q.LockSSHD(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	worker := context.WithValue(context.Background(), heldLocksKey{}, map[string]bool{})
	if _, err = q.LockSSHD(worker); !errors.Is(err, ErrRetry) {
		t.Fatalf("got %v, want ErrRetry", err)
	}
	release()
	release, err = q.LockSSHD(worker)
	if err != nil {
		t.Fatal(err)
	}
	release()
}
