package exec

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunArgvAndEnvironment(t *testing.T) {
	t.Setenv("NOVAPANEL_LEAK", "secret")
	out, stderr, exit, err := (Real{}).Run(context.Background(), "/usr/bin/printf", []string{"%s", "$(touch forbidden); literal"}, RunOpts{})
	if err != nil || exit != 0 || len(stderr) != 0 || string(out) != "$(touch forbidden); literal" {
		t.Fatalf("%q %q %d %v", out, stderr, exit, err)
	}
	out, _, _, err = (Real{}).Run(context.Background(), "/usr/bin/env", nil, RunOpts{Env: []string{"ONLY=yes"}})
	if err != nil || string(out) != "ONLY=yes\n" {
		t.Fatalf("environment %q %v", out, err)
	}
}
func TestRunIOAndExit(t *testing.T) {
	out, _, exit, err := (Real{}).Run(context.Background(), "/usr/bin/cat", nil, RunOpts{Stdin: strings.NewReader("hello"), Dir: t.TempDir()})
	if err != nil || exit != 0 || string(out) != "hello" {
		t.Fatalf("%q %d %v", out, exit, err)
	}
	_, _, exit, err = (Real{}).Run(context.Background(), "/usr/bin/false", nil, RunOpts{})
	if exit != 1 || err == nil {
		t.Fatalf("%d %v", exit, err)
	}
}
func TestRunBoundedOutput(t *testing.T) {
	out, _, _, _ := (Real{}).Run(context.Background(), "/usr/bin/printf", []string{"%s", strings.Repeat("x", 10000)}, RunOpts{MaxOutput: 16})
	if len(out) != 16 {
		t.Fatalf("len %d", len(out))
	}
}
func TestRunBoundedStreams(t *testing.T) {
	out, stderr, exit, err := (Real{}).Run(context.Background(), os.Args[0], []string{"-test.run=TestProcessHelper"}, RunOpts{Env: []string{"NOVAPANEL_HELPER=io"}, MaxOutput: 17})
	if err != nil || exit != 0 || len(out) != 17 || len(stderr) != 17 {
		t.Fatalf("%d %d %d %v", len(out), len(stderr), exit, err)
	}
}
func TestRunDropsCredentials(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to test child credential drop")
	}
	out, stderr, exit, err := (Real{}).Run(context.Background(), "/usr/bin/id", nil, RunOpts{Uid: 65534, Gid: 65534, Groups: []uint32{65534}})
	if err != nil || exit != 0 || !strings.Contains(string(out), "uid=65534") || !strings.Contains(string(out), "gid=65534") {
		t.Fatalf("%q %q %d %v", out, stderr, exit, err)
	}
}
func TestRunCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, _, err := (Real{}).Run(ctx, "/usr/bin/sleep", []string{"30"}, RunOpts{})
	if err != context.DeadlineExceeded || time.Since(start) > time.Second {
		t.Fatalf("%v", err)
	}
}
func TestRunRejectsRelativeProgram(t *testing.T) {
	_, _, _, err := (Real{}).Run(context.Background(), "env", nil, RunOpts{})
	if err == nil {
		t.Fatal("relative program accepted")
	}
}
func TestRunMissingProgram(t *testing.T) {
	_, _, exit, err := (Real{}).Run(context.Background(), t.TempDir()+"/absent", nil, RunOpts{})
	if err == nil || exit != -1 {
		t.Fatalf("%d %v", exit, err)
	}
}

// Helper processes exercise real descendant termination without a shell.
func TestProcessHelper(t *testing.T) {
	switch os.Getenv("NOVAPANEL_HELPER") {
	case "io":
		fmt.Fprint(os.Stdout, strings.Repeat("o", 10000))
		fmt.Fprint(os.Stderr, strings.Repeat("e", 10000))
		os.Exit(0)
	case "identity":
		fmt.Fprintf(os.Stdout, "%d:%d", os.Getuid(), os.Getgid())
		os.Exit(0)
	case "parent":
		p, e := os.StartProcess(os.Args[0], []string{os.Args[0], "-test.run=TestProcessHelper"}, &os.ProcAttr{Env: []string{"NOVAPANEL_HELPER=child"}, Files: []*os.File{os.Stdin, os.Stdout, os.Stderr}})
		if e != nil {
			os.Exit(2)
		}
		if e = os.WriteFile(os.Getenv("NOVAPANEL_PIDFILE"), []byte(strconv.Itoa(p.Pid)), 0600); e != nil {
			os.Exit(3)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "child":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
}
func TestCancellationKillsDescendants(t *testing.T) {
	pidfile := t.TempDir() + "/pid"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, _, e := (Real{}).Run(ctx, os.Args[0], []string{"-test.run=TestProcessHelper"}, RunOpts{Env: []string{"NOVAPANEL_HELPER=parent", "NOVAPANEL_PIDFILE=" + pidfile}})
		done <- e
	}()
	var pid int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b, e := os.ReadFile(pidfile)
		if e == nil {
			pid, _ = strconv.Atoi(string(b))
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("helper did not start")
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	cancel()
	select {
	case e := <-done:
		if e != context.Canceled {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner blocked")
	}
	for time.Now().Before(deadline) {
		b, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if os.IsNotExist(e) || strings.Contains(string(b), ") Z ") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("descendant survived cancellation")
}
