package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"novapanel/internal/audit"
	"novapanel/internal/firewall"
	"novapanel/internal/store"
)

func TestFirewallTerminalAdmissionMatrix(t *testing.T) {
	for _, state := range []string{"armed", "confirming", "reverting", "acknowledged", "absent", "malformed", "missing-lock"} {
		t.Run(state, func(t *testing.T) {
			a, j, r, close := terminalAgent(t, true)
			defer close()
			root := filepath.Dir(filepath.Dir(a.Cfg.DBPath)) + "/journal"
			switch state {
			case "absent":
				mustTerminal(t, os.Remove(root+"/state"))
			case "malformed":
				mustTerminal(t, os.WriteFile(root+"/state", []byte(`{}`), 0600))
			case "missing-lock":
				mustTerminal(t, os.Remove(root+"/lock"))
			default:
				r.State = state
				b, e := json.Marshal(r)
				mustTerminal(t, e)
				mustTerminal(t, os.WriteFile(root+"/state", b, 0600))
			}
			if a.CommitFirewallTerminal(t.Context()) == nil {
				t.Fatal("unsafe or unresolved journal admitted")
			}
			terminalCounts(t, a, 1, 0)
			_ = j
		})
	}
}

func TestFirewallTerminalBoundedCallerContext(t *testing.T) {
	a, _, _, close := terminalAgent(t, true)
	defer close()
	sentinel := errors.New("admission refused")
	type key struct{}
	ctx := context.WithValue(t.Context(), key{}, "value")
	a.firewallTerminalJournal = func(got context.Context) (*firewall.Journal, error) {
		deadline, ok := got.Deadline()
		if !ok || time.Until(deadline) > 10*time.Second || got.Value(key{}) != "value" {
			t.Fatal("context bound or values lost")
		}
		return nil, sentinel
	}
	if e := a.CommitFirewallTerminal(ctx); !errors.Is(e, sentinel) {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	a.firewallTerminalJournal = func(got context.Context) (*firewall.Journal, error) {
		if !errors.Is(got.Err(), context.Canceled) {
			t.Fatal("request cancellation detached")
		}
		return nil, got.Err()
	}
	if !errors.Is(a.CommitFirewallTerminal(ctx), context.Canceled) {
		t.Fatal("cancellation lost")
	}
}

type terminalBlockingSink struct {
	audit.Sink
	entered chan struct{}
	release chan struct{}
}

func (s *terminalBlockingSink) Sync() error { close(s.entered); <-s.release; return s.Sink.Sync() }
func TestFirewallTerminalStoreCloseDrainsLockedCommit(t *testing.T) {
	a, j, r, cleanupInitial := terminalAgent(t, true)
	defer cleanupInitial()
	mustTerminal(t, a.Audit.Close())
	f, e := os.OpenFile(a.Cfg.LogDir+"/audit.log", os.O_APPEND|os.O_WRONLY, 0600)
	mustTerminal(t, e)
	sink := &terminalBlockingSink{Sink: f, entered: make(chan struct{}), release: make(chan struct{})}
	a.Audit = audit.New(a.Store, sink)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- a.CommitFirewallTerminal(ctx) }()
	<-sink.entered
	closed := make(chan error, 1)
	go func() { closed <- a.Store.Close() }()
	select {
	case e := <-closed:
		t.Fatal("close bypassed active callback", e)
	case <-time.After(30 * time.Millisecond):
	}
	if other, e := store.Open(a.Cfg.DBPath); e == nil {
		other.Close()
		t.Fatal("lifetime ownership released during callback")
	}
	cancel()
	closeChannel := sink.release
	close(closeChannel)
	if e = <-done; e == nil {
		t.Fatal("canceled SQL committed")
	}
	mustTerminal(t, <-closed)
	// Store Close drained the operation, not the journal outcome. Reopen actual
	// daemon state only after both owners have stopped, then consume same ID.
	mustTerminal(t, a.Audit.Close())
	cleanup, e := a.OpenState(t.Context())
	mustTerminal(t, e)
	defer cleanup()
	terminalCounts(t, a, 1, 1)
	mustTerminal(t, a.CommitFirewallTerminal(t.Context()))
	terminalCounts(t, a, 0, 1)
	got, p, e := j.Inspect(t.Context())
	mustTerminal(t, e)
	if !p || got != r {
		t.Fatal(got)
	}
}
