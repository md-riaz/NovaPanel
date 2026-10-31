package firewall

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A directory flock is an independent-process barrier, not a shared Go mutex.
func TestSnapshotCustodyPublicationLock(t *testing.T) {
	c, root := custodyFixture(t)
	barrier, e := snapshotOpenDir(c.dir, ".")
	mustCustody(t, e)
	defer barrier.Close()
	mustCustody(t, unix.Flock(int(barrier.Fd()), unix.LOCK_EX|unix.LOCK_NB))
	defer unix.Flock(int(barrier.Fd()), unix.LOCK_UN)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if digest, e := c.Put(ctx, custodyID, custodyInput()); e == nil || digest != "" {
		t.Fatal("publication bypassed independent directory lock")
	}
	for _, name := range []string{custodyID, custodyID + ".next"} {
		if _, e := os.Stat(filepath.Join(root, name)); !os.IsNotExist(e) {
			t.Fatal("waiting writer changed custody", e)
		}
	}
	mustCustody(t, unix.Flock(int(barrier.Fd()), unix.LOCK_UN))
	digest, e := c.Put(context.Background(), custodyID, custodyInput())
	mustCustody(t, e)
	other, e := OpenSnapshotCustody(root)
	mustCustody(t, e)
	defer other.Close()
	again, e := other.Put(context.Background(), custodyID, custodyInput())
	mustCustody(t, e)
	if again != digest {
		t.Fatal("duplicate changed digest")
	}
	s, e := other.Verify(context.Background(), custodyRecord(digest))
	mustCustody(t, e)
	mustCustody(t, s.Close())
}

func TestSnapshotCustodyDuplicateWaitsForPublisher(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprint(shared), func(t *testing.T) {
			first, root := custodyFixture(t)
			second := first
			if !shared {
				var e error
				second, e = OpenSnapshotCustody(root)
				mustCustody(t, e)
				defer second.Close()
			}
			paused, resume := make(chan struct{}), make(chan struct{})
			var once sync.Once
			first.syncFile = func(f *os.File) error {
				once.Do(func() { close(paused); <-resume })
				return f.Sync()
			}
			done := make(chan error, 1)
			go func() { _, e := first.Put(context.Background(), custodyID, custodyInput()); done <- e }()
			<-paused
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			_, e := second.Put(ctx, custodyID, custodyInput())
			waitErr := ctx.Err()
			cancel()
			close(resume)
			mustCustody(t, <-done)
			if e == nil || waitErr != context.DeadlineExceeded {
				t.Fatal("duplicate did not wait for publisher", e, waitErr)
			}
			digest, e := second.Put(context.Background(), custodyID, custodyInput())
			mustCustody(t, e)
			s, e := second.Verify(context.Background(), custodyRecord(digest))
			mustCustody(t, e)
			mustCustody(t, s.Close())
		})
	}
}
