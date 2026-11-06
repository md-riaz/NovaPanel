package firewall

import (
	"context"
	"errors"
	"novapanel/internal/rpcerr"
	"os"
	"testing"
	"time"
)

func TestJournalRecoveryManualBeforeDeadline(t *testing.T) {
	l, r, f := runtimeFixture(t, true)
	s, e := openInstalledJournalRecovery(context.Background(), l, func() time.Time { return time.Unix(90, 0) }, newManagerFixture(t, r), f)
	mustCustody(t, e)
	defer s.Close()
	mustCustody(t, s.Revert(context.Background(), r.ID))
	if len(f.calls) != 2 {
		t.Fatal("manual recovery never reached installed restoration")
	}
}
func TestJournalRecoveryConfirmationStopsTimer(t *testing.T) {
	l, r, ufw := runtimeFixture(t, true)
	base := newManagerFixture(t, r)
	stopped := false
	runner := interceptManager{base: base, hook: func(ctx context.Context, p string, a []string) ([]byte, []byte, int, error, bool) {
		if p == "/usr/bin/systemctl" {
			if len(a) != 3 || a[1] != "stop" || a[2] != r.Unit+".timer" {
				t.Fatal(a)
			}
			stopped = true
			return nil, nil, 0, nil, true
		}
		return nil, nil, 0, nil, false
	}}
	s, e := openInstalledJournalRecovery(context.Background(), l, func() time.Time { return time.Unix(90, 0) }, runner, ufw)
	mustCustody(t, e)
	defer s.Close()
	mustCustody(t, s.Confirm(context.Background(), r.ID))
	got, e := s.journal.read()
	mustCustody(t, e)
	if !stopped || got.State != "confirmed" || len(ufw.calls) != 0 {
		t.Fatal("confirmation did not commit and stop only timer", got)
	}
}

func TestJournalRecoveryMissingLockIsNotUnknownID(t *testing.T) {
	l, r, f := runtimeFixture(t, true)
	s, e := openInstalledJournalRecovery(t.Context(), l, func() time.Time { return time.Unix(90, 0) }, newManagerFixture(t, r), f)
	mustCustody(t, e)
	defer s.Close()
	mustCustody(t, os.Remove(l.root+"/journal/lock"))
	for _, op := range []func(context.Context, string) error{s.Confirm, s.Revert} {
		e = op(t.Context(), r.ID)
		var coded *rpcerr.CodedError
		if e == nil || errors.As(e, &coded) {
			t.Fatalf("unsafe missing lock mapped to client absence: %v", e)
		}
	}
}
