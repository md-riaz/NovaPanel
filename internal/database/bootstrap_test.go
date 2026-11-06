package database

import (
	"context"
	"errors"
	"io"
	"novapanel/internal/exec"
	"strings"
	"testing"
	"time"
)

type bootstrapRunner struct {
	shape, grants string
	statements    []string
	unavailable   bool
}

func (r *bootstrapRunner) Run(_ context.Context, _ string, _ []string, o exec.RunOpts) ([]byte, []byte, int, error) {
	b, _ := io.ReadAll(o.Stdin)
	s := string(b)
	r.statements = append(r.statements, s)
	if r.unavailable {
		return nil, nil, 1, errors.New("socket missing")
	}
	switch {
	case strings.HasPrefix(s, "SELECT COUNT(*) FROM information_schema.SCHEMATA"):
		return []byte("1\n"), nil, 0, nil
	case strings.HasPrefix(s, "SELECT CONCAT("):
		return []byte(r.shape + "\n"), nil, 0, nil
	case strings.HasPrefix(s, "SELECT (\n"):
		return []byte(r.grants + "\n"), nil, 0, nil
	case strings.HasPrefix(s, "CREATE TABLE"):
		r.shape = "1:5:5:2:2:0:0:0"
	}
	return nil, nil, 0, nil
}
func TestBootstrapTablelessChecksGrantsBeforeCreate(t *testing.T) {
	for _, grants := range []string{"0", "1"} {
		r := &bootstrapRunner{shape: "0:0:0:0:0:0:0:0", grants: grants}
		err := Bootstrap(context.Background(), r)
		if grants == "1" {
			if !errors.Is(err, ErrUnsafe) {
				t.Fatalf("unsafe grants: %v", err)
			}
			for _, s := range r.statements {
				if strings.Contains(s, "CREATE TABLE") {
					t.Fatal("created table before checking grants")
				}
			}
		} else if err != nil {
			t.Fatalf("safe tableless bootstrap: %v", err)
		}
	}
}

func TestBootstrapWaitHonorsDeadline(t *testing.T) {
	bootstrapMu <- struct{}{}
	defer func() { <-bootstrapMu }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	r := &bootstrapRunner{}
	if err := Bootstrap(ctx, r); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want deadline exceeded", err)
	}
	if len(r.statements) != 0 {
		t.Fatal("ran SQL while serialization held")
	}
}

type bootstrapDeadlineRunner struct {
	deadline time.Time
	bootstrapRunner
}

func (r *bootstrapDeadlineRunner) Run(ctx context.Context, program string, args []string, opts exec.RunOpts) ([]byte, []byte, int, error) {
	r.deadline, _ = ctx.Deadline()
	return r.bootstrapRunner.Run(ctx, program, args, opts)
}

func TestBootstrapUsesCallerDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	want, _ := ctx.Deadline()
	r := &bootstrapDeadlineRunner{bootstrapRunner: bootstrapRunner{shape: "1:5:5:2:2:0:0:0", grants: "0"}}
	if err := Bootstrap(ctx, r); err != nil {
		t.Fatal(err)
	}
	if !r.deadline.Equal(want) {
		t.Fatalf("SQL deadline = %v, want caller deadline %v", r.deadline, want)
	}
}

func TestBootstrapBoundsUnboundedCaller(t *testing.T) {
	r := &bootstrapDeadlineRunner{bootstrapRunner: bootstrapRunner{shape: "1:5:5:2:2:0:0:0", grants: "0"}}
	before := time.Now()
	if err := Bootstrap(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if r.deadline.IsZero() || r.deadline.After(before.Add(6*time.Second)) {
		t.Fatalf("unbounded caller SQL deadline = %v", r.deadline)
	}
}

func TestBootstrapGrantQueryUsesExactPrincipal(t *testing.T) {
	r := &bootstrapRunner{shape: "1:5:5:2:2:0:0:0", grants: "0"}
	if err := Bootstrap(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	q := r.statements[len(r.statements)-1]
	if !strings.Contains(q, `BINARY GRANTEE <> BINARY '''root''@''localhost'''`) || strings.Count(q, "BINARY Host = BINARY 'localhost'") != 4 {
		t.Fatalf("grant scopes must exclude only root@localhost: %s", q)
	}
	for _, statement := range r.statements {
		if strings.Contains(statement, `"`) {
			t.Fatalf("bootstrap SQL must not use double-quoted literals under ANSI_QUOTES: %s", statement)
		}
	}
	shape := r.statements[1]
	for _, literal := range []string{`'enum(''schema'',''user'')'`, `'enum(''reserved'',''created'',''deleting'')'`} {
		if !strings.Contains(shape, literal) {
			t.Fatalf("missing properly escaped enum type %s: %s", literal, shape)
		}
	}
}

func TestBootstrapVerifiesBeforeTrust(t *testing.T) {
	for _, tc := range []struct {
		name, shape, grants string
		want                error
	}{
		{"valid", "1:5:5:2:2:0:0:0", "0", nil},
		{"bad shape", "1:4:4:2:2:0:0:0", "0", ErrUnsafe},
		{"privileged site", "1:5:5:2:2:0:0:0", "1", ErrUnsafe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &bootstrapRunner{shape: tc.shape, grants: tc.grants}
			err := Bootstrap(context.Background(), r)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			for _, s := range r.statements {
				if strings.Contains(s, "CREATE ") {
					t.Fatalf("altered existing schema: %s", s)
				}
			}
		})
	}
	r := &bootstrapRunner{unavailable: true}
	if !errors.Is(Bootstrap(context.Background(), r), ErrUnavailable) {
		t.Fatal("unavailable server not classified")
	}
}
