package jobs

import (
	"errors"
	"testing"
)

func TestReadyStartupRejectsLatchedRecovery(t *testing.T) {
	for _, name := range []string{"operation", "startup"} {
		t.Run(name, func(t *testing.T) {
			q, _, _, ctx := fixture(t)
			if err := q.PrepareStartup(ctx); err != nil {
				t.Fatal(err)
			}
			// Models cancellation after the recovery writer sets its latch but before
			// prepareOperationsLocked clears operationsReady.
			q.operationMu.Lock()
			if name == "operation" {
				q.recoveryBlocked = true
			} else {
				q.startupBlocked = true
			}
			q.operationMu.Unlock()
			if err := q.PrepareStartup(ctx); !errors.Is(err, ErrRecoveryBlocked) {
				t.Fatalf("latched recovery reported ready: %v", err)
			}
		})
	}
}
