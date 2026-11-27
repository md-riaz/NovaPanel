package installgate

import (
	"errors"
	"testing"
	"time"
)

type fakeDomain struct {
	exists, populated, failDrain bool
	checks, runs                 int
}

func (d *fakeDomain) empty() error {
	d.checks++
	if !d.exists || d.populated {
		return errors.New("not empty")
	}
	return nil
}
func (d *fakeDomain) drain() error {
	if d.failDrain {
		return errors.New("drain failed")
	}
	d.populated = false
	return nil
}
func (d *fakeDomain) remove() error { d.exists = false; return nil }

func TestGateLifecycle(t *testing.T) {
	d := &fakeDomain{exists: true}
	var gate Gate
	if err := gate.Do(d.empty, func() error { d.runs++; return nil }); err != nil {
		t.Fatal(err)
	}
	if d.runs != 1 || d.checks != 2 {
		t.Fatalf("runs=%d checks=%d", d.runs, d.checks)
	}
	d.populated = true
	if err := gate.Do(d.empty, func() error { t.Fatal("admitted while populated"); return nil }); err == nil {
		t.Fatal("accepted populated domain")
	}
	d.failDrain = true
	if err := gate.Close(d.drain, d.remove); err == nil || !d.exists {
		t.Fatal("failed drain removed domain")
	}
	if err := gate.Do(d.empty, func() error { t.Fatal("admission reopened"); return nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("admission: %v", err)
	}
	d.failDrain = false
	if err := gate.Close(d.drain, d.remove); err != nil || d.exists {
		t.Fatalf("close: %v", err)
	}
}

func TestGateSerializesEffects(t *testing.T) {
	var gate Gate
	d := &fakeDomain{exists: true}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() { done <- gate.Do(d.empty, func() error { close(entered); <-release; return nil }) }()
	<-entered
	competing := make(chan struct{})
	go func() { _ = gate.Do(d.empty, func() error { close(competing); return nil }) }()
	select {
	case <-competing:
		close(release)
		t.Fatal("effect overlapped")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-competing:
	case <-time.After(time.Second):
		t.Fatal("effect did not resume")
	}
}
