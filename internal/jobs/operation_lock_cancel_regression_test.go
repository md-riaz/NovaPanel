package jobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type observedContext struct {
	context.Context
	checked chan struct{}
	once    sync.Once
}

func (c *observedContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.checked) })
	return err
}
func TestOperationRecoveryCanceledAfterLockWaitBegins(t *testing.T) {
	q := NewQueue(nil, nil, &fakeAudit{})
	q.operationMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &observedContext{Context: ctx, checked: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- q.PrepareOperations(observed) }()
	<-observed.checked
	cancel()
	select {
	case err := <-done:
		q.operationMu.Unlock()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(300 * time.Millisecond):
		q.operationMu.Unlock()
		<-done
		t.Fatal("cancellation did not interrupt operation lock wait")
	}
}
