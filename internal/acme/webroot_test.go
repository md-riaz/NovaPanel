package acme_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"novapanel/internal/acme"
)

func TestWebrootLifecycle(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	p, err := acme.NewWebroot(root, []string{"example.test", "www.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	token := strings.Repeat("a", 22)
	auth := token + "." + strings.Repeat("b", 43)
	if err := p.Present("example.test", token, auth); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".well-known/acme-challenge", token)
	got, err := os.ReadFile(path)
	if err != nil || string(got) != auth {
		t.Fatalf("challenge = %q, %v", got, err)
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0644 {
		t.Fatalf("mode = %v", st.Mode())
	}
	if err := p.CleanUp("example.test", token, auth); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("challenge survived cleanup: %v", err)
	}
	if err := p.CleanUp("example.test", token, auth); err != nil {
		t.Fatal(err)
	}
}
