package firewall

import (
	"context"
	"errors"
	"testing"

	"novapanel/internal/rpcerr"
)

func TestConfirmFailsClosedBeforeTimerCancellation(t *testing.T) {
	r, s, run, snap, _ := fixture(t)
	pending := *s.p
	err := r.Confirm(context.Background(), pending.ID)
	var coded *rpcerr.CodedError
	if !errors.As(err, &coded) || coded.Code != "unsupported" {
		t.Fatalf("want unsupported without durable confirmation protocol, got %v", err)
	}
	if s.p == nil || *s.p != pending || len(run.calls) != 0 || snap.restores != 0 {
		t.Fatal("unsupported confirmation changed pending state or stopped its timer")
	}
}
