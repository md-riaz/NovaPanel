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
func TestSFTPOnlyArgumentsAndObjectKey(t *testing.T) {
	v := &sftpOnlyArgs{SiteID: "01K8WMXZF57CGNZKX1FZ38DZMA"}
	if v.Validate() == nil {
		t.Fatal("missing enabled accepted")
	}
	enabled := false
	v.Enabled = &enabled
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
	if key := rpc.Registry["sshkey.sftp_only"].ObjectKey(v); key != "site:"+v.SiteID {
		t.Fatal(key)
	}
}

func TestSSHDTeardownWithoutSystemd(t *testing.T) {
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		t.Skip("requires no systemd")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	state, root := t.TempDir(), t.TempDir()
	path := filepath.Join(root, "novapanel-demo.conf")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".lkg", []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	r := &sshdTestRunner{}
	a := &agent.Agent{Cfg: config.Config{DBPath: filepath.Join(state, "agent.db")}, Jobs: &jobs.Queue{}, Exec: r, SSHD: &sshd.Applier{Root: root, Runner: r}}
	if err := (sshdState{a: a}).apply(context.Background(), sshd.Desired{Site: "demo", User: "demo"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) || r.calls != 0 {
		t.Fatalf("file or command: %v, %d", err, r.calls)
	}
	if _, err := os.Stat(path + ".lkg"); err != nil {
		t.Fatalf("last-known-good lost: %v", err)
	}
}

func TestSSHDTeardownNoopAndReloadFailure(t *testing.T) {
	state, root, systemd := t.TempDir(), t.TempDir(), t.TempDir()
	r := &sshdTestRunner{fail: map[int]bool{1: true, 2: true, 3: true}}
	a := &agent.Agent{Cfg: config.Config{DBPath: filepath.Join(state, "agent.db")}, Jobs: &jobs.Queue{}, Exec: r, SSHD: &sshd.Applier{Root: root, Runner: r}}
	s := sshdState{a: a, systemdDir: systemd}
	d := sshd.Desired{Site: "demo", User: "demo"}
	if err := s.apply(context.Background(), d, true); err != nil || r.calls != 0 {
		t.Fatalf("absent teardown: %v calls %d", err, r.calls)
	}
	marker := filepath.Join(state, "sshd-diverged")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("unexpected marker: %v", err)
	}
	path := filepath.Join(root, "novapanel-demo.conf")
	good := []byte("old good")
	if err := os.WriteFile(path, good, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".lkg", good, 0644); err != nil {
		t.Fatal(err)
	}
	r.fail = map[int]bool{2: true, 3: true}
	if err := s.apply(context.Background(), d, true); err == nil {
		t.Fatal("expected failure")
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != string(good) {
		t.Fatalf("restore: %q %v", b, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("missing marker: %v", err)
	}
}

func TestSSHDMarkerHealsAndRefuses(t *testing.T) {
	state, root := t.TempDir(), t.TempDir()
	r := &sshdTestRunner{fail: map[int]bool{1: true, 2: true, 3: true}}
	a := &agent.Agent{Cfg: config.Config{DBPath: filepath.Join(state, "agent.db")}, Jobs: &jobs.Queue{}, Exec: r, SSHD: &sshd.Applier{Root: root, Runner: r}}
	d := sshd.Desired{Site: "demo", User: "demo", Enabled: true}
	ctx := context.Background()
	if err := (sshdState{a: a}).apply(ctx, d, false); err == nil {
		t.Fatal("expected divergence")
	}
	marker := filepath.Join(state, "sshd-diverged")
	info, err := os.Stat(marker)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("marker: %v %v", info, err)
	}
	err = (sshdState{a: a}).apply(ctx, d, false)
	var coded *rpc.CodedError
	if !errors.As(err, &coded) || coded.Code != "precondition_failed" {
		t.Fatalf("expected refusal: %v", err)
	}
	if err = (sshdState{a: a}).apply(ctx, d, false); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("marker remains: %v", err)
	}
}
