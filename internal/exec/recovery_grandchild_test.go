package exec

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Deliberately demonstrate the limit, not cgroup cleanup acceptance. The raw
// direct-child policy must not be described as descendant ownership.
func TestRecoveryRawParentDeathDoesNotOwnGrandchild(t *testing.T) {
	root, mode := os.Getenv("NP_RAW_ROOT"), os.Getenv("NP_RAW_MODE")
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	if mode == "grandchild" {
		if e := os.WriteFile(root+"/grandchild", []byte(strconv.Itoa(os.Getpid())), 0600); e != nil {
			t.Fatal(e)
		}
		for {
			if _, e := os.Stat(root + "/prove-survival"); e == nil {
				if e := os.WriteFile(root+"/survived", []byte("after direct-child death"), 0600); e != nil {
					t.Fatal(e)
				}
				return
			}
			time.Sleep(time.Millisecond)
		}
	}
	if mode == "child" {
		if e := os.WriteFile(root+"/child", []byte(strconv.Itoa(os.Getpid())), 0600); e != nil {
			t.Fatal(e)
		}
		_, _, _, _ = (Real{}).Run(context.Background(), exe, []string{"-test.run=^TestRecoveryRawParentDeathDoesNotOwnGrandchild$"}, RunOpts{Env: []string{"NP_RAW_MODE=grandchild", "NP_RAW_ROOT=" + root}})
		t.Fatal("raw child unexpectedly returned")
	}
	if mode == "parent" {
		go func() {
			for {
				if _, e := os.Stat(root + "/grandchild"); e == nil {
					_ = unix.Kill(os.Getpid(), unix.SIGKILL)
					return
				}
				time.Sleep(time.Millisecond)
			}
		}()
		_, _, _, _ = (Recovery{}).Run(context.Background(), exe, []string{"-test.run=^TestRecoveryRawParentDeathDoesNotOwnGrandchild$"}, RunOpts{Env: []string{"NP_RAW_MODE=child", "NP_RAW_ROOT=" + root}})
		t.Fatal("parent unexpectedly survived")
	}
	root = t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, _, exit, e := (Real{}).Run(ctx, exe, []string{"-test.run=^TestRecoveryRawParentDeathDoesNotOwnGrandchild$"}, RunOpts{Env: []string{"NP_RAW_MODE=parent", "NP_RAW_ROOT=" + root}})
	if e == nil || exit == 0 {
		t.Fatal("missing parent death")
	}
	b, e := os.ReadFile(root + "/grandchild")
	if e != nil {
		t.Fatal(e)
	}
	pid, e := strconv.Atoi(string(b))
	if e != nil {
		t.Fatal(e)
	}
	// This exact PID was published by the owned fixture. Always clean it up.
	defer unix.Kill(pid, unix.SIGKILL)
	childBytes, e := os.ReadFile(root + "/child")
	if e != nil {
		t.Fatal(e)
	}
	childPID, e := strconv.Atoi(string(childBytes))
	if e != nil {
		t.Fatal(e)
	}
	defer unix.Kill(childPID, unix.SIGKILL)
	deathDeadline := time.Now().Add(5 * time.Second)
	for {
		stat, e := os.ReadFile("/proc/" + strconv.Itoa(childPID) + "/stat")
		if os.IsNotExist(e) || e == nil && strings.Contains(string(stat), ") Z ") {
			break
		}
		if time.Now().After(deathDeadline) {
			t.Fatal("direct child survived parent death")
		}
		time.Sleep(time.Millisecond)
	}
	mustWrite := func(p string) {
		t.Helper()
		if e := os.WriteFile(p, nil, 0600); e != nil {
			t.Fatal(e)
		}
	}
	mustWrite(root + "/prove-survival")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, e := os.Stat(root + "/survived"); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("raw grandchild did not survive direct parent death")
		}
		time.Sleep(time.Millisecond)
	}
}
