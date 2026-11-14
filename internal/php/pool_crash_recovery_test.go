package php

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Model disk residues at the pool publication boundary. SQLite's committed
// state, not either scratch file or a published but uncheckpointed candidate,
// remains the recovery authority.
func TestPoolRecoveryPublicationResidues(t *testing.T) {
	for _, stop := range []string{"partial-temp", "candidate", "published-before-checkpoint", "after-checkpoint"} {
		t.Run(stop, func(t *testing.T) {
			ctx := context.Background()
			dir := testPoolDir(t)
			a := PoolApplier{Dir: dir, Runner: &poolRunner{}, Probe: func(context.Context) error { return nil }}
			old := PoolState{SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Exists: true, MaxChildren: 3}
			next := PoolState{SiteID: old.SiteID, Exists: true, MaxChildren: 4}
			if err := a.Recover(ctx, "demo", old); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "novapanel-demo.conf")
			candidate, err := renderState("demo", next)
			if err != nil {
				t.Fatal(err)
			}
			committed := old
			switch stop {
			case "partial-temp":
				err = os.WriteFile(path+".candidate.tmp", []byte("partial"), 0600)
			case "candidate":
				err = os.WriteFile(path+".candidate", candidate, 0600)
			case "published-before-checkpoint", "after-checkpoint":
				err = os.WriteFile(path, candidate, 0600)
				if stop == "after-checkpoint" {
					committed = next
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err := a.Recover(ctx, "demo", committed); err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				want, err := renderState("demo", committed)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != string(want) {
					t.Fatalf("recovery %d: got %q, want %q", i, got, want)
				}
			}
		})
	}
}
