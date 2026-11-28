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

// The three bind states: valid and absent are known binds, malformed is unknown. Load never fails on the bind.
func TestWebListenStates(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.toml")
	webPath := filepath.Join(dir, "web-listen")
	cases := []struct {
		name, content string
		absent        bool
		listen        string
		state         WebListenState
		known         bool
	}{
		{name: "valid non-loopback", content: "0.0.0.0:9443\n", listen: "0.0.0.0:9443", state: WebListenValid, known: true},
		{name: "valid loopback", content: "127.0.0.1:9443\n", listen: "127.0.0.1:9443", state: WebListenValid, known: true},
		{name: "absent", absent: true, listen: DefaultWebListen, state: WebListenAbsent, known: true},
		{name: "malformed", content: "not-a-bind\n", listen: DefaultWebListen, state: WebListenMalformed, known: false},
	}
	for _, tc := range cases {
		if tc.absent {
			_ = os.Remove(webPath)
		} else if err := os.WriteFile(webPath, []byte(tc.content), 0600); err != nil {
			t.Fatal(err)
		}
		c, err := load(cfgPath, webPath, nil)
		if err != nil || c.WebListen != tc.listen || c.WebListenState != tc.state || c.WebListenKnown() != tc.known {
			t.Fatalf("%s: listen %q state %q known %v err %v", tc.name, c.WebListen, c.WebListenState, c.WebListenKnown(), err)
		}
	}
	var zero Config
	if zero.WebListenKnown() {
		t.Fatal("zero config reported a known bind")
	}
}

// A malformed or unreadable bind file must never fail Load: recovery and revert load the same config.
func TestMalformedWebListenDegradesWithoutError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(cfgPath, []byte("db_path = '/var/lib/novapanel/custom.db'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	webPath := filepath.Join(dir, "web-listen")
	for _, content := range []string{"not-a-bind\n", "127.0.0.1:0\n", "127.0.0.1:70000\n", "\x00junk"} {
		if err := os.WriteFile(webPath, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		c, err := load(cfgPath, webPath, nil)
		if err != nil || c.WebListenState != WebListenMalformed || c.WebListen != DefaultWebListen {
			t.Fatalf("%q: listen %q state %q err %v", content, c.WebListen, c.WebListenState, err)
		}
		if c.DBPath != "/var/lib/novapanel/custom.db" {
			t.Fatalf("%q: db path %q", content, c.DBPath)
		}
	}
	// A directory is unreadable as a file, so it takes the same fail-safe path.
	c, err := load(cfgPath, dir, nil)
	if err != nil || c.WebListenState != WebListenMalformed || c.WebListen != DefaultWebListen {
		t.Fatalf("unreadable: listen %q state %q err %v", c.WebListen, c.WebListenState, err)
	}
}

func TestSharedWebListen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web-listen")
	if err := os.WriteFile(path, []byte("0.0.0.0:9443\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a, known, err := ReadWebListen(path)
	if err != nil || !known || a != "0.0.0.0:9443" {
		t.Fatalf("%q %v %v", a, known, err)
	}
	b, known, err := ReadWebListen(path)
	if err != nil || !known || b != a {
		t.Fatalf("web and agent differ: %q %q %v", a, b, err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	a, known, err = ReadWebListen(path)
	if err != nil || known || a != DefaultWebListen {
		t.Fatalf("missing: %q %v %v", a, known, err)
	}
}
