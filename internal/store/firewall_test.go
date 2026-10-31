package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"novapanel/internal/exec"
	"novapanel/internal/firewall"
	"novapanel/internal/rpcerr"
)

func firewallFixture(t *testing.T, s *Store) {
	t.Helper()
	b, err := os.ReadFile("testdata/firewall_pending.sql")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := os.ReadFile("../../docs/spec/02-data-model.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(spec), strings.TrimSpace(string(b))) {
		t.Fatal("fixture differs from canonical DDL")
	}
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error { _, err := tx.Exec(string(b)); return err }); err != nil {
		t.Fatal(err)
	}
}
func pendingFixture() firewall.Pending {
	return firewall.Pending{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", SnapshotPath: "/agent-owned/snapshot", RevertUnit: "novapanel-fwrevert-01ARZ3NDEKTSV4RRFFQ69G5FAV", Deadline: time.UnixMilli(2000000)}
}
func TestFirewallCancellationBeforeCommitFailureRetainsRow(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	firewallFixture(t, s)
	ctx := context.Background()
	p := pendingFixture()
	if err := s.CreateFirewallPending(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER deny_fw_delete BEFORE DELETE ON fw_pending BEGIN SELECT RAISE(ABORT,'injected failure'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	run := &pendingRunner{}
	snap := &pendingSnapshot{}
	recovery := firewall.Recovery{Store: s, Runner: run, Snapshot: snap, Now: func() time.Time { return p.Deadline.Add(-time.Second) }}
	if err := recovery.Confirm(ctx, p.ID); err == nil {
		t.Fatal("delete failure ignored")
	}
	if run.calls != 1 {
		t.Fatal("cancellation not exercised")
	}
	if err := s.WithPending(ctx, p.ID, func(firewall.Pending) (bool, error) { return false, nil }); err != nil {
		t.Fatal("recoverable row lost", err)
	}
	// The row survives, but its timer was already stopped. This test documents the
	// unresolved crash window; it is not proof that live confirm is crash-safe.
}
func TestFirewallDurableDeadmanDeadlineAndRetry(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	firewallFixture(t, s)
	ctx := context.Background()
	p := pendingFixture()
	if err := s.CreateFirewallPending(ctx, p); err != nil {
		t.Fatal(err)
	}
	run := &pendingRunner{}
	snap := &pendingSnapshot{fail: true}
	recovery := firewall.Recovery{Store: s, Runner: run, Snapshot: snap, Now: func() time.Time { return p.Deadline }}
	if err := recovery.Confirm(ctx, p.ID); firewallCode(err) != "not_found" {
		t.Fatal(err)
	}
	if run.calls != 0 {
		t.Fatal("expired confirmation stopped timer")
	}
	if err := recovery.Fire(ctx, p.ID); err == nil {
		t.Fatal("failed restore consumed")
	}
	snap.fail = false
	if err := recovery.Fire(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := recovery.Fire(ctx, p.ID); firewallCode(err) != "not_found" {
		t.Fatal("replay", err)
	}
}

func firewallCode(err error) string {
	var coded *rpcerr.CodedError
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ""
}

type pendingRunner struct{ calls int }

func (r *pendingRunner) Run(context.Context, string, []string, exec.RunOpts) ([]byte, []byte, int, error) {
	r.calls++
	return nil, nil, 0, nil
}

type pendingSnapshot struct{ fail bool }

func (s *pendingSnapshot) RestoreAndMark(context.Context, firewall.Pending) error {
	if s.fail {
		return errors.New("injected restore failure")
	}
	return nil
}

func TestFirewallPendingDurableSingletonAndConsume(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "panel.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	firewallFixture(t, s)
	p := pendingFixture()
	if err := s.CreateFirewallPending(ctx, p); err != nil {
		t.Fatal(err)
	}
	other := p
	other.ID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	other.RevertUnit = "novapanel-fwrevert-" + other.ID
	if err := s.CreateFirewallPending(ctx, other); err == nil {
		t.Fatal("second pending accepted")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sentinel := errors.New("restore failed")
	if err := s.WithPending(ctx, p.ID, func(got firewall.Pending) (bool, error) {
		if got != p {
			t.Fatalf("got %+v", got)
		}
		return true, sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	var success atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.WithPending(ctx, p.ID, func(firewall.Pending) (bool, error) { success.Add(1); return true, nil }); err == nil {
				return
			}
		}()
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatal("not one-shot", success.Load())
	}
	if err := s.WithPending(ctx, p.ID, func(firewall.Pending) (bool, error) { t.Fatal("replay callback"); return false, nil }); err == nil {
		t.Fatal("replay accepted")
	}
}
