package sshd

import (
	"context"
	"errors"
	runner "novapanel/internal/exec"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type fakeRunner struct {
	calls []string
	fail  map[int]bool
	check func(int)
}

func (r *fakeRunner) Run(_ context.Context, p string, a []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	r.calls = append(r.calls, p+" "+join(a))
	n := len(r.calls)
	if r.check != nil {
		r.check(n)
	}
	if r.fail[n] {
		return nil, nil, 1, errors.New("failed")
	}
	return nil, nil, 0, nil
}
func join(a []string) string {
	s := ""
	for i, v := range a {
		if i > 0 {
			s += " "
		}
		s += v
	}
	return s
}
func TestApplyAndRollback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "novapanel-demo.conf")
	r := &fakeRunner{}
	a := &Applier{Root: dir, Runner: r}
	d := Desired{Site: "demo", User: "demo", Enabled: true}
	want := "Match User demo\n    ForceCommand internal-sftp\n    AllowTcpForwarding no\n    X11Forwarding no\n    PermitTTY no\n"
	r.check = func(n int) {
		if n == 1 {
			b, e := os.ReadFile(path)
			if e != nil || string(b) != want {
				t.Fatalf("candidate: %q %v", b, e)
			}
		}
	}
	if err := a.Apply(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.calls, []string{"/usr/sbin/sshd -t", "/usr/bin/systemctl reload ssh.service"}) {
		t.Fatal(r.calls)
	}
	r.calls = nil
	r.check = nil
	r.fail = map[int]bool{1: true}
	d.Enabled = false
	if err := a.Apply(context.Background(), d); err == nil {
		t.Fatal("expected validation failure")
	}
	if !reflect.DeepEqual(r.calls, []string{"/usr/sbin/sshd -t", "/usr/bin/systemctl reload ssh.service"}) {
		t.Fatal(r.calls)
	}
	if b, e := os.ReadFile(path); e != nil || string(b) != want {
		t.Fatalf("restore: %q %v", b, e)
	}
	r.calls = nil
	r.fail = nil
	if err := a.Apply(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("drop-in not removed", err)
	}
}
func TestApplyDivergenceAndValidation(t *testing.T) {
	r := &fakeRunner{fail: map[int]bool{1: true, 2: true}}
	a := &Applier{Root: t.TempDir(), Runner: r}
	if err := a.Apply(context.Background(), Desired{Site: "demo", User: "demo", Enabled: true}); !errors.Is(err, ErrDiverged) {
		t.Fatal(err)
	}
	for _, d := range []Desired{{Site: "demo\nMatch User root", User: "demo", Enabled: true}, {Site: "demo", User: "root", Enabled: true}} {
		if err := a.Apply(context.Background(), d); err == nil {
			t.Fatal("accepted invalid binding")
		}
	}
}
