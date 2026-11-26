//go:build lab

package sshd

import (
	"os"
	"os/exec"
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
	cmd := exec.Command(keygen, "-q", "-t", "ed25519", "-N", "", "-f", key)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("keygen: %v %s", err, out)
	}
	r := &fakeRunner{}
	a := &Applier{Root: dir, Runner: r}
	if err := a.Apply(t.Context(), Desired{Site: "demo", User: "demo", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dir, "sshd_config")
	body := "HostKey " + key + "\nPidFile " + filepath.Join(dir, "sshd.pid") + "\nInclude " + filepath.Join(dir, "novapanel-*.conf") + "\n"
	if err := os.WriteFile(conf, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(binary, "-t", "-f", conf)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sshd -t: %v %s", err, out)
	}
	cmd = exec.Command(binary, "-T", "-f", conf, "-C", "user=demo,host=localhost,addr=127.0.0.1")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "forcecommand internal-sftp") {
		t.Fatalf("sshd -T: %v %s", err, out)
	}
}
