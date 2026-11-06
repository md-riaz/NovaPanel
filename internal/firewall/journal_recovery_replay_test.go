package firewall

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestJournalRecoveryMarkerReplayNeverSkipsCommands(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			s, r, _ := recoveryFixture(t, enabled)
			// Block terminal publication before rename, after the installed callback has
			// durably published its marker. Keep the authoritative reverting record.
			count := 0
			s.journal.syncDirectory = func() error {
				count++
				if count == 1 {
					mustCustody(t, os.Mkdir(s.layout.root+"/journal/state.next", 0700))
				}
				return s.journal.dir.Sync()
			}
			if e := s.Revert(t.Context(), r.ID); e == nil {
				t.Fatal("terminal publication unexpectedly succeeded")
			}
			got, e := s.journal.read()
			mustCustody(t, e)
			if got.State != "reverting" {
				t.Fatal(got)
			}
			// This injection blocks the *first* restoreRecord publication's later stage,
			// so the marker must actually exist before declaring the fault exercised.
			if _, e = os.Stat(s.layout.root + "/restore/markers/" + r.ID + ".json"); e != nil {
				t.Fatal("did not reach durable marker", e)
			}
			mustCustody(t, os.Remove(s.layout.root+"/journal/state.next"))
			f := &restoreRunner{}
			next, e := openInstalledJournalRecovery(t.Context(), s.layout, func() time.Time { return time.Unix(101, 0) }, newManagerFixture(t, r), f)
			mustCustody(t, e)
			defer next.Close()
			mustCustody(t, next.Revert(t.Context(), r.ID))
			want := 1
			if enabled {
				want = 2
			}
			if len(f.calls) != want {
				t.Fatal("marker suppressed commands", f.calls)
			}
		})
	}
}

func TestJournalRecoveryConfirmWinnerSuppressesFire(t *testing.T) {
	s, r, f := recoveryFixture(t, true)
	entered, release := make(chan struct{}), make(chan struct{})
	base := newManagerFixture(t, r)
	s.manager.runner = interceptManager{base: base, hook: func(_ context.Context, p string, a []string) ([]byte, []byte, int, error, bool) {
		if p == "/usr/bin/systemctl" {
			close(entered)
			<-release
			return nil, nil, 0, nil, true
		}
		return nil, nil, 0, nil, false
	}}
	confirmed := make(chan error, 1)
	go func() { confirmed <- s.Confirm(t.Context(), r.ID) }()
	<-entered
	fire := make(chan error, 1)
	go func() {
		fire <- recoverInstalled(t.Context(), r.ID, s.layout, func() time.Time { return time.Unix(101, 0) }, f)
	}()
	select {
	case e := <-fire:
		t.Fatal("Fire bypassed confirming holder", e)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	mustCustody(t, <-confirmed)
	mustCustody(t, <-fire)
	if len(f.calls) != 0 {
		t.Fatal("confirmed journal restored")
	}
}
