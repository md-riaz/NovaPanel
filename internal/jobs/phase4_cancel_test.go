package jobs

import (
	"context"
	"database/sql"
	"errors"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestOperationStartupCancellationWhileSQLiteBusy(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	held, release := make(chan struct{}), make(chan struct{})
	txDone := make(chan error, 1)
	go func() {
		txDone <- s.Tx(context.Background(), func(tx *sql.Tx) error {
			if _, e := tx.Exec("UPDATE phase4_operations SET updated=updated WHERE 0"); e != nil {
				return e
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	q := NewQueue(s, nil, &fakeAudit{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- q.PrepareOperations(ctx) }()
	cancel()
	select {
	case err = <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("startup cancellation: %v", err)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("startup did not cancel while database busy")
	}
	close(release)
	if err = <-txDone; err != nil {
		t.Fatal(err)
	}
	q.operationMu.Lock()
	ready := q.operationsReady
	q.operationMu.Unlock()
	if ready {
		t.Fatal("cancellation opened admission")
	}
}
