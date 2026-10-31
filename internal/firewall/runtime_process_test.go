package firewall

import (
	"context"
	"fmt"
	execpkg "novapanel/internal/exec"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type runtimeBarrierRunner struct{ root string }

func (f runtimeBarrierRunner) Run(ctx context.Context, _ string, _ []string, _ execpkg.RunOpts) ([]byte, []byte, int, error) {
	if e := os.WriteFile(filepath.Join(f.root, "entered-"+os.Getenv("NP_RUNTIME_LABEL")), []byte("command"), 0600); e != nil {
		return nil, nil, 1, e
	}
	if os.Getenv("NP_RUNTIME_BLOCK") == "1" {
		select {
		case <-ctx.Done():
		case <-time.After(time.Hour):
		}
		return nil, nil, 1, ctx.Err()
	}
	return nil, nil, 0, nil
}
func TestRuntimeRecoveryProcess(t *testing.T) {
	if root := os.Getenv("NP_RUNTIME_ROOT"); root != "" {
		if e := os.WriteFile(filepath.Join(root, "started-"+os.Getenv("NP_RUNTIME_LABEL")), nil, 0600); e != nil {
			t.Fatal(e)
		}
		if e := recoverInstalled(t.Context(), custodyID, runtimeLayout{root: root, rules: root + "/rules"}, func() time.Time { return time.Unix(101, 0) }, runtimeBarrierRunner{root}); e != nil {
			t.Fatal(e)
		}
		return
	}
	l, r, _ := runtimeFixture(t, true)
	exe, e := os.Executable()
	mustCustody(t, e)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	launch := func(ctx context.Context, label, block string) error {
		_, stderr, exit, e := (execpkg.Recovery{}).Run(ctx, exe, []string{"-test.run=^TestRuntimeRecoveryProcess$"}, execpkg.RunOpts{Env: []string{"NP_RUNTIME_ROOT=" + l.root, "NP_RUNTIME_LABEL=" + label, "NP_RUNTIME_BLOCK=" + block}})
		if e != nil || exit != 0 {
			return fmt.Errorf("child exit %d: %w %s", exit, e, stderr)
		}
		return nil
	}
	go func() { first <- launch(ctx, "first", "1") }()
	waitRuntimeFile(t, l.root+"/entered-first")
	secondCtx, secondCancel := context.WithCancel(context.Background())
	defer secondCancel()
	second := make(chan error, 1)
	go func() { second <- launch(secondCtx, "second", "0") }()
	waitRuntimeFile(t, l.root+"/started-second")
	// The first helper still owns journal + restore locks in its blocked command.
	select {
	case e := <-second:
		t.Fatal("competitor escaped held locks", e)
	case <-time.After(60 * time.Millisecond):
	}
	if _, e := os.Stat(l.root + "/entered-second"); !os.IsNotExist(e) {
		t.Fatal("competing command effect", e)
	}
	cancel()
	if e := <-first; e == nil {
		t.Fatal("killed helper succeeded")
	}
	select {
	case e := <-second:
		mustCustody(t, e)
	case <-time.After(10 * time.Second):
		t.Fatal("retry did not acquire released locks")
	}
	j, e := OpenJournal(l.root + "/journal")
	mustCustody(t, e)
	state, e := j.read()
	mustCustody(t, e)
	mustCustody(t, j.Close())
	if state.ID != r.ID || state.State != "reverted" {
		t.Fatal(state)
	}
	b, e := os.ReadFile(l.rules + "/user.rules")
	mustCustody(t, e)
	if string(b) != string(custodyInput().Rules["user.rules"]) {
		t.Fatal("retry did not restore bytes")
	}
}
func waitRuntimeFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, e := os.Stat(path); e == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("barrier absent", path)
}
