package firewall

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"novapanel/internal/exec"
)

type snapshotRunner struct {
	calls  [][]string
	active bool
	fail   string
}

func (r *snapshotRunner) Run(_ context.Context, path string, args []string, _ exec.RunOpts) ([]byte, []byte, int, error) {
	r.calls = append(r.calls, append([]string{path}, args...))
	if path == r.fail {
		return nil, nil, 1, nil
	}
	if len(args) > 0 && args[0] == "status" {
		if r.active {
			return []byte("Status: active\n"), nil, 0, nil
		}
		return []byte("Status: inactive\n"), nil, 0, nil
	}
	return []byte("saved rules\n"), nil, 0, nil
}

func TestSnapshotRestore(t *testing.T) {
	for _, active := range []bool{false, true} {
		root := t.TempDir()
		rules := filepath.Join(root, "rules")
		snap := filepath.Join(root, "snapshot")
		if err := os.Mkdir(filepath.Join(root, "default"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "default", "ufw"), []byte("DEFAULT_INPUT_POLICY=ACCEPT\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(rules, 0700); err != nil {
			t.Fatal(err)
		}
		for _, name := range ruleFiles {
			if err := os.WriteFile(filepath.Join(rules, name), []byte(name), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(rules, "custom.rules"), []byte("custom"), 0600); err != nil {
			t.Fatal(err)
		}
		r := &snapshotRunner{active: active}
		if err := Snapshot(context.Background(), r, rules, snap); err != nil {
			t.Fatal(err)
		}
		for _, name := range ruleFiles {
			if err := os.WriteFile(filepath.Join(rules, name), []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(rules, "custom.rules"), []byte("changed"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "default", "ufw"), []byte("changed"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := Restore(context.Background(), r, rules, snap); err != nil {
			t.Fatal(err)
		}
		for _, name := range ruleFiles {
			b, err := os.ReadFile(filepath.Join(rules, name))
			if err != nil || string(b) != name {
				t.Fatalf("%s: %q %v", name, b, err)
			}
		}
		if b, err := os.ReadFile(filepath.Join(root, "default", "ufw")); err != nil || string(b) != "DEFAULT_INPUT_POLICY=ACCEPT\n" {
			t.Fatalf("default policy: %q %v", b, err)
		}
		if b, err := os.ReadFile(filepath.Join(rules, "custom.rules")); err != nil || string(b) != "custom" {
			t.Fatalf("custom: %q %v", b, err)
		}
		last := r.calls[len(r.calls)-1]
		if active && last[1] != "reload" || !active && last[1] != "--force" {
			t.Fatalf("wrong restore: %v", r.calls)
		}
	}
}

func TestSnapshotFailureDoesNotRestore(t *testing.T) {
	root := t.TempDir()
	rules := filepath.Join(root, "rules")
	snap := filepath.Join(root, "snapshot")
	if err := os.Mkdir(filepath.Join(root, "default"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "default", "ufw"), []byte("DEFAULT_INPUT_POLICY=ACCEPT\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(rules, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range ruleFiles {
		if err := os.WriteFile(filepath.Join(rules, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r := &snapshotRunner{fail: "/usr/sbin/iptables-save"}
	if err := Snapshot(context.Background(), r, rules, snap); err == nil {
		t.Fatal("snapshot unexpectedly succeeded")
	}
	if err := Restore(context.Background(), r, rules, snap); err == nil {
		t.Fatal("restored incomplete snapshot")
	}
}
