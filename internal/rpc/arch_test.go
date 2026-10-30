package rpc_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	runner "novapanel/internal/exec"
	"novapanel/internal/rpc"
	_ "novapanel/internal/verbs"
	"novapanel/tools/arch"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate arch test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

func TestArchitecture(t *testing.T) {
	for _, problem := range arch.Check(repoRoot(t)) {
		t.Error(problem)
	}
}

func TestRegisteredVerbs(t *testing.T) {
	if len(rpc.Registry) == 0 {
		t.Fatal("empty built registry: seed verbs were not imported")
	}
	expected := []string{"ping", "agent.info", "service.status", "service.restart", "job.status", "job.list", "job.claim", "reconcile", "schedule.list", "schedule.set", "schedule.enable", "schedule.disable"}
	if len(rpc.Registry) != len(expected) {
		t.Fatalf("Phase 1 allowlist mismatch: %d", len(rpc.Registry))
	}
	for _, name := range expected {
		if _, ok := rpc.Registry[name]; !ok {
			t.Fatalf("missing verb %s", name)
		}
	}
	names := map[string]bool{}
	for name, verb := range rpc.Registry {
		if name == "" || verb.Name != name || names[verb.Name] {
			t.Errorf("invalid or duplicate registry name %q", name)
		}
		names[verb.Name] = true
		if verb.Run == nil {
			t.Errorf("%s has no Run handler", name)
		}
	}
	for _, name := range []string{"ping", "agent.info"} {
		if !names[name] {
			t.Errorf("missing seed verb %s", name)
		}
	}
}

func TestArchitectureNegativeFixtures(t *testing.T) {
	goBin := "/usr/bin/env" // Resolve Go from PATH without importing os/exec.
	for _, tc := range []struct{ name, want string }{{"bad-spawn", "process import"}, {"bad-shell", "shell execution"}, {"bad-privilege", "privileged client"}, {"bad-root", "root refusal"}, {"bad-registration", "unregistered"}, {"bad-duplicate", "duplicate registered verb"}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			out, stderr, exit, err := (runner.Real{}).Run(ctx, goBin, []string{"go", "test", "-mod=readonly", "-count=1", "-run", "^TestArchitecture$", "./..."}, runner.RunOpts{Dir: filepath.Join(repoRoot(t), "internal/rpc/testdata", tc.name), Env: os.Environ(), MaxOutput: 1 << 20})
			text := string(out) + string(stderr)
			if exit == 0 {
				t.Fatalf("negative fixture unexpectedly passed: %s", text)
			}
			if !strings.Contains(text, tc.want) || !strings.Contains(text, "--- FAIL: TestArchitecture") {
				t.Fatalf("fixture failed for wrong reason (exit=%d err=%v): %s", exit, err, text)
			}
		})
	}
}
