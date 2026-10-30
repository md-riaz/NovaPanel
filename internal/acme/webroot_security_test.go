package acme_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"novapanel/internal/acme"
)

func TestWebrootRejectsHostileChallengesAndNeverClobbersFiles(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0755)
	p, e := acme.NewWebroot(root, []string{"example.test"})
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	token := strings.Repeat("a", 22)
	auth := token + "." + strings.Repeat("b", 43)
	for _, c := range []struct{ domain, token, auth string }{
		{"other.test", token, auth}, {"example.test", "../escape", "../escape." + strings.Repeat("b", 43)},
		{"example.test", strings.Repeat("a", 129), strings.Repeat("a", 129) + "." + strings.Repeat("b", 43)},
		{"example.test", token, "wrong." + strings.Repeat("b", 43)},
		{"example.test", token, token + "." + strings.Repeat("b", 42) + "\n"},
		{"example.test", "short", "short." + strings.Repeat("b", 43)},
	} {
		if e := p.Present(c.domain, c.token, c.auth); e == nil {
			t.Errorf("accepted hostile challenge %+v", c)
		}
	}
	path := filepath.Join(root, ".well-known/acme-challenge", token)
	if e := os.WriteFile(path, []byte("existing"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := p.Present("example.test", token, auth); e == nil {
		t.Fatal("overwrote existing token")
	}
	if e := p.CleanUp("example.test", token, auth); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "existing" {
		t.Fatal("removed or changed unowned token")
	}
	os.Remove(path)
	target := filepath.Join(t.TempDir(), "target")
	os.WriteFile(target, []byte("protected"), 0600)
	if e := os.Symlink(target, path); e != nil {
		t.Fatal(e)
	}
	if e := p.Present("example.test", token, auth); e == nil {
		t.Fatal("followed token symlink")
	}
	b, _ = os.ReadFile(target)
	if string(b) != "protected" {
		t.Fatal("changed symlink target")
	}
}

func TestWebrootRejectsUnsafeDirectories(t *testing.T) {
	for _, kind := range []string{"root-symlink", "child-symlink", "writable", "private", "wildcard", "duplicates", "too-many"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			os.Chmod(root, 0755)
			domains := []string{"example.test"}
			switch kind {
			case "root-symlink":
				link := filepath.Join(t.TempDir(), "root")
				os.Symlink(root, link)
				root = link
			case "child-symlink":
				os.Symlink(t.TempDir(), filepath.Join(root, ".well-known"))
			case "writable":
				os.Chmod(root, 0777)
			case "private":
				os.Chmod(root, 0700)
			case "wildcard":
				domains = []string{"*.example.test"}
			case "duplicates":
				domains = []string{"example.test", "EXAMPLE.test"}
			case "too-many":
				domains = make([]string, 101)
			}
			p, e := acme.NewWebroot(root, domains)
			if e == nil {
				p.Close()
				t.Fatal("accepted unsafe webroot or domains")
			}
		})
	}
}

func TestWebrootSharedTokenAndCloseCleanup(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0755)
	p, e := acme.NewWebroot(root, []string{"example.test", "www.example.test"})
	if e != nil {
		t.Fatal(e)
	}
	token := strings.Repeat("c", 22)
	auth := token + "." + strings.Repeat("d", 43)
	if e := p.Present("example.test", token, auth); e != nil {
		t.Fatal(e)
	}
	if e := p.Present("www.example.test", token, auth); e != nil {
		t.Fatal(e)
	}
	if e := p.CleanUp("example.test", token, auth); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, ".well-known/acme-challenge", token)
	if _, e := os.Stat(path); e != nil {
		t.Fatal("removed another domain's shared challenge")
	}
	if e := p.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("Close leaked challenge")
	}
	if e := p.Present("example.test", token, auth); e == nil {
		t.Fatal("used closed provider")
	}
}
