package jobs

import (
	"context"
	"errors"
	"testing"
)

func TestCanceledEmptyLockSetDoesNotAdmit(t *testing.T) {
	var k keyedLock
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release, err := k.lockSetContext(ctx, nil)
	if release != nil {
		release()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled empty lock set admitted: %v", err)
	}
}
