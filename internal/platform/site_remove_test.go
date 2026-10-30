package platform_test

import (
	"context"
	runner "novapanel/internal/exec"
	"novapanel/internal/platform"
	"os"
	"path/filepath"
	"testing"
)

type removalRunner struct {
	home    string
	deleted bool
	calls   int
}

func (r *removalRunner) Run(_ context.Context, prog string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	r.calls++
	if prog == "/usr/bin/getent" {
		if r.deleted {
			return nil, nil, 2, nil
		}
		return []byte("demo:x:1234:1234::" + r.home + ":/usr/sbin/nologin\n"), nil, 0, nil
	}
	if prog == "/usr/sbin/userdel" && len(args) == 2 && args[0] == "--" && args[1] == "demo" {
		if _, e := os.Stat(r.home); !os.IsNotExist(e) {
			return nil, nil, 1, nil
		}
		r.deleted = true
		return nil, nil, 0, nil
	}
	return nil, nil, 1, nil
}
func TestRemoveSiteConfinedAndRetrySafe(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "demo")
	outside := t.TempDir()
	if e := os.WriteFile(filepath.Join(outside, "keep"), []byte("safe"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(filepath.Join(home, "public"), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(home, "public", "link")); e != nil {
		t.Fatal(e)
	}
	r := &removalRunner{home: home}
	for range 2 {
		if e := platform.RemoveSite(context.Background(), r, root, "demo"); e != nil {
			t.Fatal(e)
		}
	}
	if !r.deleted {
		t.Fatal("account retained")
	}
	if _, e := os.Stat(filepath.Join(outside, "keep")); e != nil {
		t.Fatal("followed site symlink", e)
	}
	if e := os.Symlink(outside, home); e != nil {
		t.Fatal(e)
	}
	first := r.calls
	if e := platform.RemoveSite(context.Background(), r, root, "demo"); e == nil {
		t.Fatal("accepted symlink home")
	}
	if r.calls != first {
		t.Fatal("ran command before validation")
	}
}
