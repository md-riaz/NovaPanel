package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pexec "novapanel/internal/exec"
)

func TestTLSRootSharedParentTraversal(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires dropped UID")
	}
	base := t.TempDir()
	if err := os.Chmod(base, 0755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "etc", "novapanel", "tls")
	if err := prepareTLSRoot(root); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Dir(filepath.Dir(root)), filepath.Dir(root)} {
		info, err := os.Stat(dir)
		if err != nil || info.Mode().Perm() != 0755 {
			t.Fatalf("shared parent must be 0755: %s: %v %v", dir, info, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "private.key"), []byte("private test bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	probe := func(present bool) {
		t.Helper()
		script := `import os, errno, sys
base_fd = os.open('.', os.O_RDONLY)
os.setgroups([]); os.setgid(65534); os.setuid(65534)
try:
 fd = os.open('etc/novapanel/agent.toml', os.O_RDONLY, dir_fd=base_fd)
except OSError as e:
 assert sys.argv[1] == 'absent' and e.errno == errno.ENOENT, e
else:
 assert sys.argv[1] == 'present'
 assert os.read(fd, 100) == b'public config'
 os.close(fd)
for path in ['etc/novapanel/tls', 'etc/novapanel/tls/private.key']:
 try:
  os.open(path, os.O_RDONLY, dir_fd=base_fd)
 except OSError as e:
  assert e.errno == errno.EACCES, e
 else:
  raise AssertionError('private TLS path readable: ' + path)
`
		out, stderr, _, err := (pexec.Real{}).Run(context.Background(), "/usr/bin/python3", []string{"-", map[bool]string{false: "absent", true: "present"}[present]}, pexec.RunOpts{Dir: base, Stdin: strings.NewReader(script)})
		if err != nil {
			t.Fatalf("dropped UID probe: %v: %s %s", err, out, stderr)
		}
	}
	probe(false)
	if err := os.WriteFile(filepath.Join(filepath.Dir(root), "agent.toml"), []byte("public config"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(filepath.Dir(root), "agent.toml"), 0644); err != nil {
		t.Fatal(err)
	}
	probe(true)
}

func TestTLSRootPreservesExistingSharedParent(t *testing.T) {
	base := t.TempDir()
	parent := filepath.Join(base, "configured")
	if err := os.Mkdir(parent, 0750); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareTLSRoot(filepath.Join(parent, "tls")); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(parent)
	if err != nil || before.Mode() != after.Mode() || !os.SameFile(before, after) {
		t.Fatal("existing parent changed", err)
	}
}
