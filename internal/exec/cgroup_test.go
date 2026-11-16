package exec

import (
	"context"
	"os"
	"testing"
)

func TestCgroupRejectsOrdinaryDirectory(t *testing.T) {
	f, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if c, err := NewCgroup(f); err == nil {
		c.Close()
		t.Fatal("ordinary directory accepted")
	}
}

func TestCgroupKernelPlacementRefusesWithoutDelegation(t *testing.T) {
	if os.Getenv("NOVAPANEL_CGROUP_REFUSAL_LAB") != "1" {
		t.Skip("confined nondelegated container only")
	}
	f, err := os.Open("/sys/fs/cgroup")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c, err := NewCgroup(f)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	out, _, exit, err := (Real{}).Run(context.Background(), "/usr/bin/printf", []string{"executed"}, RunOpts{Cgroup: c})
	if err == nil || exit != -1 || len(out) != 0 {
		t.Fatalf("unexpected delegated spawn: %q %d %v", out, exit, err)
	}
	t.Logf("CLONE_INTO_CGROUP refused without fallback: %v", err)
}

func TestRunClosedCgroupNeverFallsBack(t *testing.T) {
	c := &Cgroup{}
	out, _, exit, err := (Real{}).Run(context.Background(), "/usr/bin/printf", []string{"executed"}, RunOpts{Cgroup: c})
	if err == nil || exit != -1 || len(out) != 0 {
		t.Fatalf("placement fallback: %q %d %v", out, exit, err)
	}
}
