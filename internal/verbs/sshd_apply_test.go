package verbs

import (
	"context"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/config"
	runner "novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/render/sshd"
	"novapanel/internal/rpc"
	"os"
	"path/filepath"
	"testing"
)

type sshdTestRunner struct {
	calls int
	fail  map[int]bool
}

func (r *sshdTestRunner) Run(context.Context, string, []string, runner.RunOpts) ([]byte, []byte, int, error) {
	r.calls++
	if r.fail[r.calls] {
		return nil, nil, 1, errors.New("failed")
	}
	return nil, nil, 0, nil
}
func TestSSHDMarkerHealsAndRefuses(t *testing.T) {
	state, root := t.TempDir(), t.TempDir()
	r := &sshdTestRunner{fail: map[int]bool{1: true, 2: true, 3: true}}
	a := &agent.Agent{Cfg: config.Config{DBPath: filepath.Join(state, "agent.db")}, Jobs: &jobs.Queue{}, Exec: r, SSHD: &sshd.Applier{Root: root, Runner: r}}
	d := sshd.Desired{Site: "demo", User: "demo", Enabled: true}
	ctx := context.Background()
	if err := applySSHD(ctx, a, d, false); err == nil {
		t.Fatal("expected divergence")
	}
	marker := filepath.Join(state, "sshd-diverged")
	info, err := os.Stat(marker)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("marker: %v %v", info, err)
	}
	err = applySSHD(ctx, a, d, false)
	var coded *rpc.CodedError
	if !errors.As(err, &coded) || coded.Code != "precondition_failed" {
		t.Fatalf("expected refusal: %v", err)
	}
	if err = applySSHD(ctx, a, d, false); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("marker remains: %v", err)
	}
}
