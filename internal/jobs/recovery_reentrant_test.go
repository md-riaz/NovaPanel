package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRecoveryRejectsMutationLeaseUpgrade(t *testing.T) {
	q, _, _, ctx := fixture(t)
	if err := q.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	held, release, err := q.AdmitMutation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, tc := range []struct {
		name string
		run  func(context.Context) error
	}{{"operations", q.PrepareOperations}, {"startup", q.PrepareStartup}} {
		t.Run(tc.name, func(t *testing.T) {
			deadline, cancel := context.WithTimeout(held, 300*time.Millisecond)
			defer cancel()
			err := tc.run(deadline)
			if !errors.Is(err, ErrRecoveryBlocked) {
				t.Fatalf("reentrant recovery did not fail closed: %v", err)
			}
		})
	}
}
