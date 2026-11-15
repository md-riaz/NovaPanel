package database

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"novapanel/internal/exec"
)

type stopBeforeBootstrapTable struct {
	delegate exec.Runner
	stopped  bool
}

func (r *stopBeforeBootstrapTable) Run(ctx context.Context, program string, args []string, opts exec.RunOpts) ([]byte, []byte, int, error) {
	b, err := io.ReadAll(opts.Stdin)
	if err != nil {
		return nil, nil, -1, err
	}
	opts.Stdin = strings.NewReader(string(b))
	if strings.HasPrefix(string(b), "CREATE TABLE IF NOT EXISTS `novapanel`") {
		r.stopped = true
		return nil, nil, 1, nil
	}
	return r.delegate.Run(ctx, program, args, opts)
}

func TestLiveBootstrapInterruptedReplayAndUnsafeGrant(t *testing.T) {
	if os.Getenv("NOVAPANEL_MARIADB_REPLAY_TEST") != "1" {
		t.Skip("requires disposable MariaDB replay fixture")
	}
	marker, err := os.ReadFile("/run/novapanel-replay-fixture")
	if os.Getenv("NOVAPANEL_MARIADB_DISPOSABLE") != "network-none" || err != nil || strings.TrimSpace(string(marker)) != "disposable-network-none" {
		t.Fatal("disposable network-none fixture required")
	}
	ctx := context.Background()
	r := exec.Real{}
	// Dedicated fixture only: do not destroy an existing control schema.
	state, err := runSQL(ctx, r, "SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='novapanel';")
	if err != nil || state != "0" {
		t.Fatalf("fixture must start without control schema: %q %v", state, err)
	}
	t.Cleanup(func() {
		if _, err := runSQL(ctx, r, "DROP DATABASE IF EXISTS `novapanel`; DROP USER IF EXISTS 'np_bootstrap_test'@'localhost';"); err != nil {
			t.Error(err)
		}
	})
	interrupted := &stopBeforeBootstrapTable{delegate: r}
	if err := Bootstrap(ctx, interrupted); err == nil || !interrupted.stopped {
		t.Fatalf("missing simulated interruption: %v", err)
	}
	shape, err := bootstrapShape(ctx, r)
	if err != nil || shape != "0:0:0:0:0:0:0:0" {
		t.Fatalf("interrupted shape %q: %v", shape, err)
	}
	if _, err = runSQL(ctx, r, "CREATE USER 'np_bootstrap_test'@'localhost'; GRANT SELECT ON `novapanel`.* TO 'np_bootstrap_test'@'localhost';"); err != nil {
		t.Fatal(err)
	}
	if err = Bootstrap(ctx, r); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("unsafe grant accepted: %v", err)
	}
	shape, err = bootstrapShape(ctx, r)
	if err != nil || shape != "0:0:0:0:0:0:0:0" {
		t.Fatalf("unsafe schema modified %q: %v", shape, err)
	}
	if _, err = runSQL(ctx, r, "DROP USER 'np_bootstrap_test'@'localhost';"); err != nil {
		t.Fatal(err)
	}
	if err = Bootstrap(ctx, r); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err = Bootstrap(ctx, r); err != nil {
		t.Fatalf("repeat: %v", err)
	}
}
