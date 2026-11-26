package sshd

import (
	"context"
	"errors"
	runner "novapanel/internal/exec"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"golang.org/x/sys/unix"
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
	if err := a.Apply(context.Background(), d); err != nil || len(r.calls) != 0 {
		t.Fatalf("unchanged apply: %v, calls %v", err, r.calls)
	}
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
func TestReplayAppliesFromLastKnownGood(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "novapanel-demo.conf")
	d := Desired{Site: "demo", User: "demo", Enabled: true}
	want := content(d)
	if err := os.WriteFile(path, want, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".lkg", []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{}
	a := &Applier{Root: dir, Runner: r}
	if err := a.Apply(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.calls, []string{"/usr/sbin/sshd -t", "/usr/bin/systemctl reload ssh.service"}) {
		t.Fatalf("candidate replay commands: %v", r.calls)
	}
	b, err := os.ReadFile(path + ".lkg")
	if err != nil || string(b) != string(want) {
		t.Fatalf("promoted: %q %v", b, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	d.Enabled = false
	r.calls = nil
	if err := a.Apply(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.calls, []string{"/usr/sbin/sshd -t", "/usr/bin/systemctl reload ssh.service"}) {
		t.Fatalf("removal replay commands: %v", r.calls)
	}
	if _, err := os.Stat(path + ".lkg"); !os.IsNotExist(err) {
		t.Fatalf("last-known-good still present: %v", err)
	}
}

func TestApplyReplacesSymlinkAndThenNoops(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "novapanel-demo.conf")
	target := filepath.Join(dir, "unmanaged")
	d := Desired{Site: "demo", User: "demo", Enabled: true}
	if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".lkg", content(d), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{}
	a := &Applier{Root: dir, Runner: r}
	if err := a.Apply(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path); err != nil || !reflect.DeepEqual(b, content(d)) {
		t.Fatalf("drop-in: %q %v", b, err)
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("not regular: %v %v", info, err)
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "untouched" {
		t.Fatalf("target: %q %v", b, err)
	}
	if !reflect.DeepEqual(r.calls, []string{"/usr/sbin/sshd -t", "/usr/bin/systemctl reload ssh.service"}) {
		t.Fatal(r.calls)
	}
	r.calls = nil
	if err := a.Apply(context.Background(), d); err != nil || len(r.calls) != 0 {
		t.Fatalf("steady state: %v %v", err, r.calls)
	}
}

func TestApplyRepairsSpecialAndUnreadableFiles(t *testing.T) {
	for _, kind := range []string{"fifo", "mode000"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "novapanel-demo.conf")
			d := Desired{Site: "demo", User: "demo", Enabled: true}
			if err := os.WriteFile(path+".lkg", content(d), 0644); err != nil {
				t.Fatal(err)
			}
			if kind == "fifo" {
				if err := unix.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, content(d), 0000); err != nil {
				t.Fatal(err)
			}
			r := &fakeRunner{}
			a := &Applier{Root: dir, Runner: r}
			done := make(chan error, 1)
			go func() { done <- a.Apply(context.Background(), d) }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("apply blocked on live drop-in")
			}
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() == 0 {
				t.Fatalf("not readable regular file: %v %v", info, err)
			}
			if b, err := os.ReadFile(path); err != nil || !reflect.DeepEqual(b, content(d)) {
				t.Fatalf("drop-in: %q %v", b, err)
			}
			if !reflect.DeepEqual(r.calls, []string{"/usr/sbin/sshd -t", "/usr/bin/systemctl reload ssh.service"}) {
				t.Fatal(r.calls)
			}
			r.calls = nil
			if err := a.Apply(context.Background(), d); err != nil || len(r.calls) != 0 {
				t.Fatalf("steady state: %v %v", err, r.calls)
			}
		})
	}
}

func TestOfflineTeardownThenEnableRestoresRestriction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "novapanel-demo.conf")
	r := &fakeRunner{}
	a := &Applier{Root: dir, Runner: r}
	enabled := Desired{Site: "demo", User: "demo", Enabled: true}
	if err := a.Apply(context.Background(), enabled); err != nil {
		t.Fatal(err)
	}
	r.calls = nil
	if err := a.Apply(context.Background(), Desired{Site: "demo", User: "demo", Offline: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("drop-in remains: %v", err)
	}
	if b, err := os.ReadFile(path + ".lkg"); err != nil || !reflect.DeepEqual(b, content(enabled)) {
		t.Fatalf("last-known-good: %q %v", b, err)
	}
	if len(r.calls) != 0 {
		t.Fatalf("offline commands: %v", r.calls)
	}
	if err := a.Apply(context.Background(), enabled); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path); err != nil || !reflect.DeepEqual(b, content(enabled)) {
		t.Fatalf("restriction missing: %q %v", b, err)
	}
	if !reflect.DeepEqual(r.calls, []string{"/usr/sbin/sshd -t", "/usr/bin/systemctl reload ssh.service"}) {
		t.Fatalf("enable commands: %v", r.calls)
	}
}

func TestReplayRestoresLastKnownGood(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "novapanel-demo.conf")
	good := []byte("Match User demo\n    ForceCommand internal-sftp\n")
	if err := os.WriteFile(path, []byte("unvalidated candidate"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".lkg", good, 0644); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{fail: map[int]bool{1: true}}
	r.check = func(n int) {
		if n == 2 {
			b, err := os.ReadFile(path)
			if err != nil || string(b) != string(good) {
				t.Fatalf("reload saw %q: %v", b, err)
			}
		}
	}
	a := &Applier{Root: dir, Runner: r}
	if err := a.Apply(context.Background(), Desired{Site: "demo", User: "demo", Enabled: true}); err == nil {
		t.Fatal("expected failure")
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != string(good) {
		t.Fatalf("restored %q: %v", b, err)
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
