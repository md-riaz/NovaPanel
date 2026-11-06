package firewall

import (
	"context"
	"errors"
	"fmt"
	execpkg "novapanel/internal/exec"
	"os"
	"testing"
	"time"
)

type commandFaultRunner struct {
	at, calls int
	fault     string
	cancel    context.CancelFunc
}

func (f *commandFaultRunner) Run(ctx context.Context, _ string, _ []string, _ execpkg.RunOpts) ([]byte, []byte, int, error) {
	f.calls++
	if f.calls != f.at {
		return nil, nil, 0, nil
	}
	switch f.fault {
	case "exit":
		return nil, nil, 7, nil
	case "error":
		return nil, nil, 0, errors.New("owned fake command failure")
	case "cancel":
		f.cancel()
		return nil, nil, 1, ctx.Err()
	}
	panic("unknown fault")
}
func TestRuntimeCommandPositionReplay(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		count := 1
		if enabled {
			count = 2
		}
		for pos := 1; pos <= count; pos++ {
			for _, fault := range []string{"exit", "error", "cancel"} {
				t.Run(fmt.Sprintf("enabled-%t-position-%d-%s", enabled, pos, fault), func(t *testing.T) {
					l, r, _ := runtimeFixture(t, enabled)
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					f := &commandFaultRunner{at: pos, fault: fault, cancel: cancel}
					now := func() time.Time { return time.Unix(r.Deadline, 0) }
					if e := recoverInstalled(ctx, r.ID, l, now, f); e == nil {
						t.Fatal("failed command accepted")
					}
					if f.calls != pos {
						t.Fatalf("commands %d want %d", f.calls, pos)
					}
					j, e := OpenJournal(l.root + "/journal")
					mustCustody(t, e)
					state, e := j.read()
					mustCustody(t, e)
					mustCustody(t, j.Close())
					if state.State != "reverting" || state.ID != r.ID || state.SnapshotSHA256 != r.SnapshotSHA256 {
						t.Fatal(state)
					}
					if _, e := os.Stat(l.root + "/restore/markers/" + r.ID + ".json"); !os.IsNotExist(e) {
						t.Fatal("failure published marker", e)
					}
					c, e := OpenSnapshotCustody(l.root + "/snapshots")
					mustCustody(t, e)
					s, e := c.Verify(t.Context(), r)
					mustCustody(t, e)
					mustCustody(t, s.Close())
					mustCustody(t, c.Close())
					good := &restoreRunner{}
					mustCustody(t, recoverInstalled(t.Context(), r.ID, l, now, good))
					if len(good.calls) != count {
						t.Fatal("retry skipped commands")
					}
					j, e = OpenJournal(l.root + "/journal")
					mustCustody(t, e)
					state, e = j.read()
					mustCustody(t, e)
					mustCustody(t, j.Close())
					if state.State != "reverted" || state.ID != r.ID {
						t.Fatal(state)
					}
				})
			}
		}
	}
}
