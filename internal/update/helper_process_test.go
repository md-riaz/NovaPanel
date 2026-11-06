package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	runner "novapanel/internal/exec"
	"novapanel/internal/store"
)

// Only subprocess tests use environment-selected modes. No production helper
// accepts these settings or any fake service runner.
func TestHelperScratchProcess(t *testing.T) {
	mode := os.Getenv("UPDATE_SCRATCH_MODE")
	if mode == "" {
		return
	}
	root := os.Getenv("UPDATE_SCRATCH_ROOT")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if mode == "parent" {
		go func() {
			(runner.Real{}).Run(context.Background(), exe, []string{"-test.run=^TestHelperScratchProcess$"}, runner.RunOpts{Env: []string{"UPDATE_SCRATCH_MODE=survivor", "UPDATE_SCRATCH_ROOT=" + root, "UPDATE_SCRATCH_SOCKET=" + os.Getenv("UPDATE_SCRATCH_SOCKET")}})
		}()
		waitHelperFile(t, filepath.Join(root, "child-ready"))
		os.Exit(0)
	}
	if mode == "survivor" {
		parent := unix.Getppid()
		if err := os.WriteFile(filepath.Join(root, "child-ready"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for unix.Getppid() == parent && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if unix.Getppid() == parent {
			t.Fatal("parent did not exit")
		}
	}
	b := tarBytes(t, binaryHeaders())
	sum := sha256.Sum256(b)
	a := Artifact{Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:])}
	err = apply(context.Background(), root, "1.2.3", a, bytes.NewReader(b), &fakeRunner{}, func(ctx context.Context) error {
		if mode == "crash" {
			if err := os.WriteFile(filepath.Join(root, "child-ready"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
				return err
			}
			select {}
		}
		return Ping(ctx, os.Getenv("UPDATE_SCRATCH_SOCKET"), 30)
	})
	// A stalled real Unix ping must cause rollback even after the launcher exits.
	if mode == "survivor" && err != nil {
		target, _ := os.Readlink(filepath.Join(root, "current"))
		if target != "1.0.0" {
			t.Fatal(target, err)
		}
		if e := os.WriteFile(filepath.Join(root, "survived"), []byte("parent exited; real ping deadline; rollback complete"), 0600); e != nil {
			t.Fatal(e)
		}
		return
	}
	t.Fatal("unexpected child outcome", err)
}

func waitHelperFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for", path)
}

func helperProcessRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "1.0.0"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("1.0.0", filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestHelperOutlivesLauncherWithRealPingDeadline(t *testing.T) {
	root := helperProcessRoot(t)
	socket := filepath.Join(t.TempDir(), "ping.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		c, e := l.Accept()
		if e == nil {
			defer c.Close()
			<-stop
		}
	}()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, stderr, exit, err := (runner.Real{}).Run(ctx, exe, []string{"-test.run=^TestHelperScratchProcess$"}, runner.RunOpts{Env: []string{"UPDATE_SCRATCH_MODE=parent", "UPDATE_SCRATCH_ROOT=" + root, "UPDATE_SCRATCH_SOCKET=" + socket}})
	if err != nil || exit != 0 {
		t.Fatalf("%v %d %s %s", err, exit, out, stderr)
	}
	waitHelperFile(t, filepath.Join(root, "survived"))
}

func TestHelperStoreLifetimeRemainsABlocker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	daemon, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Close()
	helper, err := store.Open(path)
	if err == nil {
		helper.Close()
		t.Fatal("helper stole live daemon store")
	}
	if err = daemon.Close(); err != nil {
		t.Fatal(err)
	}
	helper, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = helper.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHelperKilledAfterSwitchRecoversLocally(t *testing.T) {
	root := helperProcessRoot(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, _, e := (runner.Real{}).Run(ctx, exe, []string{"-test.run=^TestHelperScratchProcess$"}, runner.RunOpts{Env: []string{"UPDATE_SCRATCH_MODE=crash", "UPDATE_SCRATCH_ROOT=" + root}})
		done <- e
	}()
	waitHelperFile(t, filepath.Join(root, "child-ready"))
	if target, _ := os.Readlink(filepath.Join(root, "current")); target != "1.2.3" {
		t.Fatal(target)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("kill not observed")
	}
	if err := recoverApply(context.Background(), root, &fakeRunner{}); err != nil {
		t.Fatal(err)
	}
	if target, _ := os.Readlink(filepath.Join(root, "current")); target != "1.0.0" {
		t.Fatal(target)
	}
	if _, err := os.Stat(filepath.Join(root, ".update-pending")); !os.IsNotExist(err) {
		t.Fatal(fmt.Sprint(err))
	}
}
