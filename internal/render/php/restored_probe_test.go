package php

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestApplyProbesRestoredService(t *testing.T) {
	for _, prior := range []bool{false, true} {
		t.Run(map[bool]string{false: "first_create", true: "prior_service"}[prior], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := &applyRunner{}
			d := Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001}
			a := &Applier{Root: t.TempDir(), Runner: r, Probe: func(context.Context, Desired) error { return nil }}
			if prior {
				if err := a.Apply(ctx, d); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			restoredErr := errors.New("restored service unhealthy")
			a.Probe = func(c context.Context, got Desired) error {
				calls++
				if calls == 1 {
					cancel()
					return errors.New("candidate unhealthy")
				}
				if c.Err() != nil {
					t.Fatal("recovery inherited cancellation")
				}
				deadline, ok := c.Deadline()
				if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 10*time.Second {
					t.Fatal("recovery not bounded")
				}
				if got.Name != d.Name || got.Version != d.Version || got.UID != d.UID {
					t.Fatal("wrong restored service identity")
				}
				return restoredErr
			}
			d.Limits = &Limits{MemoryMB: 512, UploadMB: 64, MaxExecSec: 60}
			err := a.Apply(ctx, d)
			if err == nil {
				t.Fatal("expected candidate failure")
			}
			want := 1
			if prior {
				want = 2
				if !errors.Is(err, restoredErr) {
					t.Fatal("lost restored probe error", err)
				}
			}
			if calls != want {
				t.Fatalf("probe count %d want %d", calls, want)
			}
		})
	}
}
