package exec

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRecoveryParentDeath(t *testing.T) {
	mode := os.Getenv("NP_DEATH_MODE")
	root := os.Getenv("NP_DEATH_ROOT")
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	if mode == "child" {
		if e := os.WriteFile(root+"/pid", []byte(strconv.Itoa(os.Getpid())), 0600); e != nil {
			t.Fatal(e)
		}
		for {
			if e := os.WriteFile(root+"/pulse", []byte(time.Now().String()), 0600); e != nil {
				t.Fatal(e)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if mode == "parent" {
		go func() {
			for {
				if _, e := os.Stat(root + "/pulse"); e == nil {
					_ = unix.Kill(os.Getpid(), unix.SIGKILL)
					return
				}
				time.Sleep(time.Millisecond)
			}
		}()
		_, _, _, _ = (Recovery{}).Run(context.Background(), exe, []string{"-test.run=^TestRecoveryParentDeath$"}, RunOpts{Env: []string{"NP_DEATH_MODE=child", "NP_DEATH_ROOT=" + root}})
		t.Fatal("parent unexpectedly survived")
	}
	root = t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _, exit, e := (Real{}).Run(ctx, exe, []string{"-test.run=^TestRecoveryParentDeath$"}, RunOpts{Env: []string{"NP_DEATH_MODE=parent", "NP_DEATH_ROOT=" + root}})
	if e == nil || exit == 0 {
		t.Fatal("parent kill missing")
	}
	b, e := os.ReadFile(root + "/pid")
	if e != nil {
		t.Fatal(e)
	}
	pid, e := strconv.Atoi(string(b))
	if e != nil {
		t.Fatal(e)
	}
	defer unix.Kill(pid, unix.SIGKILL)
	deadline := time.Now().Add(5 * time.Second)
	for {
		stat, e := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
		// An orphan may remain a zombie until container PID1 reaps it.
		dead := os.IsNotExist(e) || (e == nil && strings.Contains(string(stat), ") Z "))
		if dead {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("direct child survived parent death")
		}
		time.Sleep(5 * time.Millisecond)
	}
	before, e := os.ReadFile(root + "/pulse")
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(30 * time.Millisecond)
	after, e := os.ReadFile(root + "/pulse")
	if e != nil || string(before) != string(after) {
		t.Fatal("post-death scratch write", e)
	}
}
