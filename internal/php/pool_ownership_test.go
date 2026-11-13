package php

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLastPoolStopsAndNextStarts(t *testing.T) {
	dir := testPoolDir(t)
	var calls []string
	r := &poolRunner{hook: func(_ context.Context, _ int, args []string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}}
	a := PoolApplier{Dir: dir, Runner: r, Probe: func(context.Context) error { return nil }}
	state := PoolState{SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Exists: true, MaxChildren: 3}
	absent := PoolState{SiteID: state.SiteID}
	if err := a.Recover(context.Background(), "demo", state); err != nil {
		t.Fatal(err)
	}
	calls = nil
	if err := a.ApplyCommitted(context.Background(), "demo", absent, state); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, "|") != "stop php8.3-fpm.service|disable php8.3-fpm.service" {
		t.Fatalf("last removal: %v", calls)
	}
	calls = nil
	if err := a.ApplyCommitted(context.Background(), "demo", state, absent); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(calls, "|"), "enable --now php8.3-fpm.service") {
		t.Fatalf("not restarted: %v", calls)
	}
}

func TestRemovingLastManagedPoolKeepsUnmanagedService(t *testing.T) {
	dir := testPoolDir(t)
	if err := os.WriteFile(filepath.Join(dir, "www.conf"), []byte("[www]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	r := &poolRunner{hook: func(_ context.Context, _ int, args []string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}}
	a := PoolApplier{Dir: dir, Runner: r, Probe: func(context.Context) error { return nil }}
	state := PoolState{SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Exists: true, MaxChildren: 3}
	if err := a.Recover(context.Background(), "demo", state); err != nil {
		t.Fatal(err)
	}
	calls = nil
	if err := a.ApplyCommitted(context.Background(), "demo", PoolState{SiteID: state.SiteID}, state); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(calls, "|")
	if strings.Contains(joined, "stop ") || strings.Contains(joined, "disable ") || !strings.Contains(joined, "reload ") {
		t.Fatalf("operator pool disrupted: %v", calls)
	}
}

func TestManagedPoolOwnership(t *testing.T) {
	dir := testPoolDir(t)
	a := PoolApplier{Dir: dir, Runner: &poolRunner{}, Probe: func(context.Context) error { return nil }}
	state := PoolState{SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Exists: true, MaxChildren: 3}
	path := filepath.Join(dir, "novapanel-www.conf")
	if err := os.WriteFile(path, []byte("operator config"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.Recover(context.Background(), "www", state); err == nil {
		t.Fatal("adopted unmanaged file")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "operator config" {
		t.Fatal("clobbered file")
	}
	os.Remove(path)
	if err := a.Recover(context.Background(), "www", state); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if !strings.HasPrefix(string(b), "; novapanel-managed site="+state.SiteID+"\n") {
		t.Fatalf("missing ownership: %s", b)
	}
	other := state
	other.SiteID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	if err := a.Recover(context.Background(), "www", other); err == nil {
		t.Fatal("accepted foreign owner")
	}
}
