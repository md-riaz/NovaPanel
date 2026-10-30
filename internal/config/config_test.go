package config

import (
	"os"
	"os/user"
	"path/filepath"
	"testing"
)

func TestLoadPrecedence(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agent.toml")
	if e := os.WriteFile(p, []byte("socket_path = '/file.sock'\nssh_port = 2222\nweb_user = 'root'\n"), 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("NOVAPANEL_SOCKET_PATH", "/env.sock")
	c, e := Load(p, map[string]string{"socket_path": "/flag.sock"})
	if e != nil || c.SocketPath != "/flag.sock" || c.SSHPort != 2222 || c.IdleSec != 600 {
		t.Fatalf("%+v %v", c, e)
	}
	c, e = Load(p, nil)
	if e != nil || c.SocketPath != "/env.sock" {
		t.Fatalf("%+v %v", c, e)
	}
	u, e := user.Current()
	if e != nil {
		t.Fatal(e)
	}
	c.WebUser = u.Username
	if e = c.ResolveWebUser(); e != nil {
		t.Fatal(e)
	}
	if c.WebUID != uint32(os.Getuid()) || c.WebGID != uint32(os.Getgid()) {
		t.Fatalf("%+v", c)
	}
}
func TestInvalidConfig(t *testing.T) {
	for _, content := range []string{"unknown = 1", "ssh_port = 0", "idle_sec = -1", "socket_path = 'relative'", "acme_use_staging = 'true'"} {
		p := filepath.Join(t.TempDir(), "config.toml")
		if e := os.WriteFile(p, []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := Load(p, nil); e == nil {
			t.Errorf("accepted %q", content)
		}
	}
	t.Setenv("NOVAPANEL_SSH_PORT", "oops")
	if _, e := Load(filepath.Join(t.TempDir(), "missing"), nil); e == nil {
		t.Fatal("bad env accepted")
	}
}
func TestDefaults(t *testing.T) {
	c, e := Load(filepath.Join(t.TempDir(), "missing"), nil)
	if e != nil || c.SocketPath != "/run/novapanel/agent.sock" || c.DeadlineDefaultMs != 120000 || c.RateLimitBurst != 20 {
		t.Fatalf("%+v %v", c, e)
	}
}
