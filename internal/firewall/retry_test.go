package firewall

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRetryBacksOffUntilSuccess(t *testing.T) {
	calls := 0
	var waits []time.Duration
	err := Retry(context.Background(), 0, time.Millisecond, 2*time.Millisecond, func() error {
		calls++
		if calls < 4 {
			return errors.New("busy")
		}
		return nil
	}, func(_ error, wait time.Duration) { waits = append(waits, wait) })
	if err != nil || calls != 4 {
		t.Fatalf("err %v calls %d", err, calls)
	}
	if want := []time.Duration{time.Millisecond, 2 * time.Millisecond, 2 * time.Millisecond}; !reflect.DeepEqual(waits, want) {
		t.Fatalf("waits %v want %v", waits, want)
	}
}

func TestRetryStopsAtAttemptLimit(t *testing.T) {
	calls := 0
	err := Retry(context.Background(), 3, 0, 0, func() error {
		calls++
		return errors.New("still failing")
	}, nil)
	if err == nil || calls != 3 {
		t.Fatalf("err %v calls %d", err, calls)
	}
}
