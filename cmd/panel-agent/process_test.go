package main

import (
	"context"
	"novapanel/internal/exec"
	"os"
	"testing"
)

// This entrypoint exists only in the test binary. Production has no bypass flag.
func TestSafeAgentProcess(t *testing.T) {
	if os.Getenv("NOVAPANEL_SAFE_TEST_PROCESS") != "1" {
		return
	}
	commandRunner = safeProcessRunner{}
	args := os.Args
	for i, s := range args {
		if s == "--" {
			if e := run(args[i+1:]); e != nil {
				t.Fatal(e)
			}
			return
		}
	}
	t.Fatal("missing helper args")
}

type safeProcessRunner struct{}

func (safeProcessRunner) Run(_ context.Context, p string, args []string, _ exec.RunOpts) ([]byte, []byte, int, error) {
	if p == "/usr/bin/chattr" && len(args) == 2 && args[0] == "+a" {
		return nil, nil, 0, nil
	}
	if p == "/usr/bin/systemctl" && len(args) == 2 {
		return []byte("active\n"), nil, 0, nil
	}
	return nil, nil, -1, os.ErrPermission
}
