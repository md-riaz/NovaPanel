package deploy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityLifecycle(t *testing.T) {
	home := t.TempDir()
	key := []byte("test-private-key")
	if err := Apply(Request{Home: home, Operation: "prepare", Key: key}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".novapanel", "deploy", "id_ed25519")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(key) {
		t.Fatal("identity mismatch", err)
	}
	if err = Apply(Request{Home: home, Operation: "remove"}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("identity remains: %v", err)
	}
}

func TestTeardownDoesNotFollowSymlink(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	marker := filepath.Join(outside, "marker")
	if err := os.WriteFile(marker, []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, ".novapanel"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".novapanel", "deploy")); err != nil {
		t.Fatal(err)
	}
	if err := Apply(Request{Home: home, Operation: "teardown"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
}
