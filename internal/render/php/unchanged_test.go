package php

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReviewIdenticalApplyDoesNotReload(t *testing.T) {
	r := &applyRunner{}
	probes := 0
	a := &Applier{Root: t.TempDir(), Runner: r, Probe: func(context.Context, Desired) error { probes++; return nil }}
	d := Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001}
	if err := a.Apply(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	r.calls = nil
	probes = 0
	if err := a.Apply(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 0 || probes != 0 {
		t.Fatalf("identical proven tuple repeated external effects: commands=%q probes=%d", r.calls, probes)
	}
}

func TestReviewStaleLiveAndTempsRetainProvenLKG(t *testing.T) {
	root := t.TempDir()
	r := &applyRunner{}
	a := &Applier{Root: root, Runner: r, Probe: func(context.Context, Desired) error { return nil }}
	d := Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001}
	if err := a.Apply(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(filepath.Join(root, "demo.conf.lkg"))
	if err != nil {
		t.Fatal(err)
	}
	d.Limits = &Limits{MemoryMB: 512, UploadMB: 64, MaxExecSec: 60}
	next, err := Render(d)
	if err != nil {
		t.Fatal(err)
	}
	next = append([]byte("; novapanel managed pool\n"), next...)
	for _, n := range []string{"demo.conf", "demo.conf.tmp", "demo.conf.lkg.tmp", "novapanel-validate.ini.tmp"} {
		if err := os.WriteFile(filepath.Join(root, n), next, 0644); err != nil {
			t.Fatal(err)
		}
	}
	r.calls = nil
	r.fail = 2
	if err := a.Apply(context.Background(), d); err == nil {
		t.Fatal("reload failure accepted")
	}
	for _, n := range []string{"demo.conf", "demo.conf.lkg"} {
		b, err := os.ReadFile(filepath.Join(root, n))
		if err != nil || !bytes.Equal(b, old) {
			t.Fatalf("%s lost proven LKG: %v", n, err)
		}
	}
	if len(r.calls) != 4 {
		t.Fatalf("expected validation/reload plus restore validation/reload: %q", r.calls)
	}
}
