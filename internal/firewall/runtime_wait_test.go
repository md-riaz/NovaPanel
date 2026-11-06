package firewall

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestRuntimeInitialLockDeadline(t *testing.T) {
	l, r, f := runtimeFixture(t, true)
	j, e := OpenJournal(l.root + "/journal")
	mustCustody(t, e)
	defer j.Close()
	entered, release, holder := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		holder <- j.locked(context.Background(), func() error { close(entered); <-release; return nil })
	}()
	<-entered
	defer func() { close(release); mustCustody(t, <-holder) }()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- recoverInstalled(ctx, r.ID, l, func() time.Time { return time.Unix(100, 0) }, f) }()
	select {
	case e := <-done:
		if !errors.Is(e, context.DeadlineExceeded) {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("initial lock ignored deadline")
	}
	if len(f.calls) != 0 {
		t.Fatal("command while lock held")
	}
}

func TestRuntimeLazyDestinationStillRefusesUnsafe(t *testing.T) {
	for _, attack := range []string{"absent", "symlink", "gid", "mode"} {
		t.Run(attack, func(t *testing.T) {
			l, r, f := runtimeFixture(t, true)
			switch attack {
			case "absent":
				mustCustody(t, os.Rename(l.root+"/restore", l.root+"/offline"))
			case "symlink":
				mustCustody(t, os.Rename(l.root+"/restore", l.root+"/offline"))
				mustCustody(t, os.Symlink("offline", l.root+"/restore"))
			case "gid":
				mustCustody(t, os.Chown(l.root+"/restore/markers", 0, 1))
			case "mode":
				mustCustody(t, os.Chmod(l.root+"/restore/markers", 0770))
			}
			if recoverInstalled(t.Context(), r.ID, l, func() time.Time { return time.Unix(100, 0) }, f) == nil {
				t.Fatal("unsafe destination admitted")
			}
			if len(f.calls) != 0 {
				t.Fatal("command before destination admission")
			}
		})
	}
}
