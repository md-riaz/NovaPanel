//go:build lab

package sshd

import (
	runner "novapanel/internal/exec"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLabSSHDConfig(t *testing.T) {
	binary := os.Getenv("NOVAPANEL_LAB_SSHD")
	keygen := os.Getenv("NOVAPANEL_LAB_SSH_KEYGEN")
	if binary == "" || keygen == "" {
		t.Skip("lab sshd and ssh-keygen not configured")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "hostkey")
	run := func(prog string, args ...string) ([]byte, error) {
		out, stderr, code, err := (runner.Real{}).Run(t.Context(), prog, args, runner.RunOpts{Env: os.Environ()})
		if err != nil || code != 0 {
			t.Fatalf("%s: %v %s", prog, err, stderr)
		}
		return out, err
	}
	run(keygen, "-q", "-t", "ed25519", "-N", "", "-f", key)
	r := &fakeRunner{}
	a := &Applier{Root: dir, Runner: r}
	if err := a.Apply(t.Context(), Desired{Site: "demo", User: "demo", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dir, "sshd_config")
	body := "HostKey " + key + "\nPidFile " + filepath.Join(dir, "sshd.pid") + "\nInclude " + filepath.Join(dir, "novapanel-*.conf") + "\nPort 2222\n"
	if err := os.WriteFile(conf, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	run(binary, "-t", "-f", conf)
	out, err := run(binary, "-T", "-f", conf, "-C", "user=demo,host=localhost,addr=127.0.0.1")
	if err != nil || !strings.Contains(string(out), "forcecommand internal-sftp") {
		t.Fatalf("sshd -T: %v %s", err, out)
	}
}
