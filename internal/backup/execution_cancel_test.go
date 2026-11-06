package backup

import (
	"context"
	"errors"
	"testing"

	"novapanel/internal/secret"
)

func TestInitRepositoryCancelledBeforeInvocation(t *testing.T) {
	sealer, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	password, err := GeneratePassword(sealer)
	if err != nil {
		t.Fatal(err)
	}
	defer password.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run := &fakeRestic{}
	repo := Repository{ID: "r", Name: "r", Kind: "local", Role: "panel", Location: "/srv/recovery"}
	if err := InitRepository(ctx, run, repo, password); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if run.calls != 0 {
		t.Fatalf("cancelled initialization invoked restic %d times", run.calls)
	}
}
