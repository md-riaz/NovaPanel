//go:build lab && linux

package platform

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runner "novapanel/internal/exec"
	"novapanel/internal/id"
)

func TestInstallDomainLab(t *testing.T) {
	if os.Getenv("NOVAPANEL_DISPOSABLE_UID_LAB") != "1" || os.Geteuid() != 0 {
		t.Skip("requires disposable root cgroup lab")
	}
	site, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		t.Fatal(err)
	}
	own, err := ownCgroup(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	d, err := CreateInstallDomain(site)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer func() {
		if !d.closed {
			_ = d.Close(ctx)
		}
	}()
	childCtx, stopChild := context.WithCancel(ctx)
	childDone := make(chan struct{})
	go func() {
		defer close(childDone)
		_, _, _, _ = d.domain.Run(childCtx, "/usr/bin/sleep", []string{"10"}, runner.RunOpts{Uid: 65534, Gid: 65534})
	}()
	for {
		empty, err := d.domain.empty()
		if err != nil {
			stopChild()
			t.Fatal(err)
		}
		if !empty {
			break
		}
		select {
		case <-childDone:
			stopChild()
			t.Fatal("child exited before admission check")
		case <-ctx.Done():
			stopChild()
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	if _, _, _, err := d.Run(ctx, "/usr/bin/true", nil, runner.RunOpts{}); err == nil {
		stopChild()
		t.Fatal("admitted child into populated domain")
	}
	stopChild()
	<-childDone
	out, stderr, code, err := d.Run(ctx, "/usr/bin/cat", []string{"/proc/self/cgroup"}, runner.RunOpts{Uid: 65534, Gid: 65534})
	if err != nil || code != 0 || !strings.Contains(string(out), "/novapanel-install/"+site+"\n") {
		t.Fatalf("membership: %q %q %d %v", out, stderr, code, err)
	}
	if err := d.Empty(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join("/sys/fs/cgroup", own, "novapanel-install", site)); !os.IsNotExist(err) {
		t.Fatalf("domain remains: %v", err)
	}
}
