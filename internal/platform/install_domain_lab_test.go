//go:build lab && linux

package platform

import (
	"context"
	"errors"
	"fmt"
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
	if os.Getenv("NOVAPANEL_CGROUP_READONLY_LAB") == "1" {
		t.Skip("requires writable delegated cgroup")
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
	type result struct {
		d   *InstallDomain
		err error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() { <-start; d, err := CreateInstallDomain(site); results <- result{d, err} }()
	}
	close(start)
	a, b := <-results, <-results
	if (a.err == nil) == (b.err == nil) {
		t.Fatalf("expected one owner: %v / %v", a.err, b.err)
	}
	d := a.d
	if d == nil {
		d = b.d
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	closed := false
	defer func() {
		if !closed {
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
	if err != nil || code != 0 || strings.TrimSpace(string(out)) != "0::"+filepath.Join(own, "novapanel-install", site) {
		t.Fatalf("membership: %q %q %d %v", out, stderr, code, err)
	}
	if err := d.Empty(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("/sys/fs/cgroup", own, "novapanel-install", site)
	childPath := filepath.Join(path, "child")
	if err := os.Mkdir(childPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := d.Empty(); err == nil {
		t.Fatal("non-leaf domain reported empty")
	}
	if _, _, _, err := d.Run(ctx, "/usr/bin/true", nil, runner.RunOpts{}); err == nil {
		t.Fatal("admitted child into non-leaf")
	}
	if err := d.WithEmpty(ctx, func(context.Context) error { t.Fatal("effect on non-leaf"); return nil }); err == nil {
		t.Fatal("accepted effect on non-leaf")
	}
	if err := os.Remove(childPath); err != nil {
		t.Fatal(err)
	}
	if err := d.WithEmpty(ctx, func(effectCtx context.Context) error {
		if err := os.Mkdir(childPath, 0755); err != nil {
			return err
		}
		defer os.Remove(childPath)
		child, err := OpenExecutionDomain(childPath)
		if err != nil {
			return err
		}
		defer child.Close()
		if _, _, _, err := child.Run(effectCtx, "/usr/bin/true", nil, runner.RunOpts{Uid: 65534, Gid: 65534}); err == nil {
			return errors.New("descendant admission crossed parent effect")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.WithEmpty(ctx, func(context.Context) error { return os.Mkdir(childPath, 0755) }); err == nil {
		t.Fatal("post-effect non-leaf accepted")
	}
	if err := os.Remove(childPath); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := d.WithEmpty(ctx, func(context.Context) error { calls++; return nil }); err != nil || calls != 1 {
		t.Fatalf("synchronous empty effect: calls=%d err=%v", calls, err)
	}
	alternate, err := OpenExecutionDomain(path)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	effectDone := make(chan error, 1)
	go func() {
		effectDone <- d.WithEmpty(ctx, func(context.Context) error { close(entered); <-release; return nil })
	}()
	<-entered
	if other, err := CreateInstallDomain(site); err == nil {
		close(release)
		t.Fatalf("created second owner during effect: %+v", other)
	}
	if _, _, _, err := alternate.Run(ctx, "/usr/bin/true", nil, runner.RunOpts{Uid: 65534, Gid: 65534}); err == nil {
		close(release)
		alternate.Close()
		t.Fatal("alternate handle admitted a child during effect")
	}
	if err := alternate.Close(); err != nil {
		close(release)
		t.Fatal(err)
	}
	type competingResult struct {
		kind string
		err  error
	}
	competing := make(chan competingResult, 2)
	go func() {
		_, _, code, err := d.Run(ctx, "/usr/bin/true", nil, runner.RunOpts{Uid: 65534, Gid: 65534})
		if err == nil && code != 0 {
			err = fmt.Errorf("child exit %d", code)
		}
		competing <- competingResult{"child", err}
	}()
	go func() {
		err := d.WithEmpty(ctx, func(context.Context) error { return nil })
		competing <- competingResult{"effect", err}
	}()
	select {
	case got := <-competing:
		close(release)
		t.Fatalf("%s crossed held effect", got.kind)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	if err := <-effectDone; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case got := <-competing:
			if got.err != nil {
				t.Fatalf("%s failed: %v", got.kind, got.err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	started, unblock, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		_ = d.WithEmpty(ctx, func(context.Context) error { close(started); <-unblock; return nil })
	}()
	<-started
	short, shortCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if err := d.Close(short); err == nil {
		shortCancel()
		close(unblock)
		t.Fatal("closed while effect was active")
	}
	shortCancel()
	if other, err := CreateInstallDomain(site); err == nil {
		close(unblock)
		t.Fatalf("lost ownership after failed drain: %+v", other)
	}
	close(unblock)
	<-finished
	if err := d.Close(ctx); err != nil {
		t.Fatal(err)
	}
	closed = true
	if _, err := os.Stat(filepath.Join("/sys/fs/cgroup", own, "novapanel-install", site)); !os.IsNotExist(err) {
		t.Fatalf("domain remains: %v", err)
	}
	// An empty, root-owned crash remnant is reopened under a new sole owner.
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	remnant, err := OpenExecutionDomain(path)
	if err != nil {
		t.Fatal(err)
	}
	_, doneAdmission, err := remnant.admit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := OpenExecutionDomain(path); err == nil {
		other.Close()
		doneAdmission()
		t.Fatal("opened domain with active admission")
	}
	if other, err := CreateInstallDomain(site); err == nil {
		other.Close(ctx)
		doneAdmission()
		t.Fatal("adopted domain with active admission")
	}
	doneAdmission()
	remnantCtx, stopRemnant := context.WithCancel(ctx)
	remnantDone := make(chan struct{})
	go func() {
		defer close(remnantDone)
		_, _, _, _ = remnant.Run(remnantCtx, "/usr/bin/sleep", []string{"10"}, runner.RunOpts{Uid: 65534, Gid: 65534})
	}()
	for {
		empty, err := remnant.empty()
		if err != nil {
			stopRemnant()
			t.Fatal(err)
		}
		if !empty {
			break
		}
		select {
		case <-remnantDone:
			t.Fatal("remnant child exited")
		case <-ctx.Done():
			stopRemnant()
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	if other, err := CreateInstallDomain(site); err == nil {
		stopRemnant()
		t.Fatalf("adopted populated crash remnant: %+v", other)
	}
	if other, err := OpenExecutionDomain(path); err == nil {
		other.Close()
		stopRemnant()
		t.Fatal("opened populated active domain")
	}
	stopRemnant()
	<-remnantDone
	if err := remnant.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if err := remnant.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := CreateInstallDomain(site)
	if err != nil {
		t.Fatal(err)
	}
	if err := recovered.Close(ctx); err != nil {
		t.Fatal(err)
	}
	unremovable, err := CreateInstallDomain(site)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(path, "leftover"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := unremovable.Close(ctx); err == nil {
		t.Fatal("removed nonempty cgroup directory")
	}
	if reopened, err := CreateInstallDomain(site); err == nil {
		reopened.Close(ctx)
		t.Fatal("adopted non-leaf crash remnant")
	}
	if err := unremovable.Close(ctx); err == nil {
		t.Fatal("old handle retried after detachment")
	}
	if err := os.Remove(filepath.Join(path, "leftover")); err != nil {
		t.Fatal(err)
	}
	reopened, err := CreateInstallDomain(site)
	if err != nil {
		t.Fatalf("ownership retained after failed unlink: %v", err)
	}
	if err := reopened.Close(ctx); err != nil {
		t.Fatal(err)
	}
	closeFailure, err := CreateInstallDomain(site)
	if err != nil {
		t.Fatal(err)
	}
	originalClose := closeFailure.closeDomain
	closeFailure.closeDomain = func() error { return errors.Join(originalClose(), errors.New("injected close failure")) }
	if err := closeFailure.Close(ctx); err == nil {
		t.Fatal("missing close error")
	}
	afterError, err := CreateInstallDomain(site)
	if err != nil {
		t.Fatalf("ownership retained after post-unlink close error: %v", err)
	}
	if err := afterError.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestInstallDomainReadOnlyLab(t *testing.T) {
	if os.Getenv("NOVAPANEL_DISPOSABLE_UID_LAB") != "1" || os.Getenv("NOVAPANEL_CGROUP_READONLY_LAB") != "1" || os.Geteuid() != 0 {
		t.Skip("requires read-only cgroup lab")
	}
	site, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if d, err := CreateInstallDomain(site); err == nil {
			t.Fatalf("read-only cgroup accepted: %+v", d)
		}
	}
}
