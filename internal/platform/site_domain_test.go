package platform

import (
	"context"
	"errors"
	runner "novapanel/internal/exec"
	"testing"
	"time"
)

type blockedDomainRunner struct {
	entered chan struct{}
	release chan struct{}
}

func (b blockedDomainRunner) Run(ctx context.Context, _ string, _ []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	close(b.entered)
	<-ctx.Done()
	<-b.release
	return nil, nil, -1, ctx.Err()
}
func TestDomainCloseAdmissionDrainsBeforeKill(t *testing.T) {
	b := blockedDomainRunner{make(chan struct{}), make(chan struct{})}
	killed := make(chan struct{}, 1)
	d := newExecutionDomain(b, nil, func(context.Context) error { killed <- struct{}{}; return nil })
	done := make(chan struct{})
	go func() { defer close(done); d.Run(context.Background(), "/unused", nil, runner.RunOpts{}) }()
	<-b.entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := d.Drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain: %v", err)
	}
	select {
	case <-killed:
		t.Fatal("kill before admitted call returned")
	default:
	}
	if _, _, _, err := d.Run(context.Background(), "/unused", nil, runner.RunOpts{}); err == nil {
		t.Fatal("retained launch admitted")
	}
	close(b.release)
	<-done
	if err := d.Drain(domainDeadline(t)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-killed:
	default:
		t.Fatal("missing process retirement")
	}
}
func TestDomainRefusesForeignCapability(t *testing.T) {
	d := newExecutionDomain(nil, nil, func(context.Context) error { return nil })
	if _, _, _, err := d.Run(context.Background(), "/unused", nil, runner.RunOpts{Cgroup: new(runner.Cgroup)}); err == nil {
		t.Fatal("foreign domain accepted")
	}
}
func TestDomainDrainFailureKeepsAdmissionClosed(t *testing.T) {
	failure := errors.New("not empty")
	d := newExecutionDomain(nil, nil, func(context.Context) error { return failure })
	if err := d.Drain(domainDeadline(t)); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, _, _, err := d.Run(context.Background(), "/unused", nil, runner.RunOpts{}); err == nil {
		t.Fatal("reopened")
	}
}
func TestDomainConcurrentDrainHasBoundedWait(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	d := newExecutionDomain(nil, nil, func(context.Context) error { close(entered); <-release; return nil })
	done := make(chan error, 1)
	go func() { done <- d.Drain(domainDeadline(t)) }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := d.Drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("concurrent drain: %v", err)
	}
	if err := d.Close(); err == nil {
		t.Fatal("closed active retirement descriptors")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestDomainMissingDeadlineStillClosesAdmission(t *testing.T) {
	d := newExecutionDomain(nil, nil, func(context.Context) error { t.Fatal("unbounded retirement"); return nil })
	if err := d.Drain(context.Background()); err == nil {
		t.Fatal("unbounded drain")
	}
	if _, _, _, err := d.Run(context.Background(), "/unused", nil, runner.RunOpts{}); err == nil {
		t.Fatal("reopened")
	}
}

func TestDomainRecursiveEmptyObservation(t *testing.T) {
	for _, text := range []string{"", "frozen 1\n", "populated 0\npopulated 0\n", "populated 2\n", "populated\n"} {
		if empty, err := parseDomainEmpty(text); err == nil || empty {
			t.Fatalf("accepted malformed events %q", text)
		}
	}
	if empty, err := parseDomainEmpty("populated 1\nfrozen 0\n"); err != nil || empty {
		t.Fatalf("populated subtree: %v %v", empty, err)
	}
	if empty, err := parseDomainEmpty("populated 0\nfrozen 0\n"); err != nil || !empty {
		t.Fatalf("empty subtree: %v %v", empty, err)
	}
}

func domainDeadline(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestOpenDomainRefusesOrdinaryDirectory(t *testing.T) {
	if d, err := OpenExecutionDomain(t.TempDir()); err == nil {
		d.Close()
		t.Fatal("ordinary directory accepted")
	}
}
