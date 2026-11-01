package php

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	runner "novapanel/internal/exec"
)

// F8 requires the service's full live configuration, including unmanaged pools
// and global settings, to validate before reloading it. A managed-only include
// file cannot establish that postcondition.
func TestPHPFullServiceValidationFailureDoesNotReload(t *testing.T) {
	for _, transactional := range []bool{false, true} {
		t.Run(map[bool]string{false: "apply", true: "coordinator_transaction"}[transactional], func(t *testing.T) {
			r := &fullValidationRunner{}
			a := &Applier{Root: t.TempDir(), Runner: r, Probe: func(context.Context, Desired) error {
				t.Fatal("probed a service whose full configuration was rejected")
				return nil
			}}
			d := Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001}
			var err error
			if transactional {
				err = a.Transaction(context.Background(), d, func(p *Transaction) error {
					if err := p.Stage(); err != nil {
						return err
					}
					err := p.Validate(context.Background())
					if err != nil {
						return errors.Join(err, p.Restore())
					}
					return p.Reload(context.Background())
				})
			} else {
				err = a.Apply(context.Background(), d)
			}
			if err == nil {
				t.Fatal("accepted invalid full service configuration")
			}
			if r.validations != 1 {
				t.Fatalf("full validations = %d, want 1", r.validations)
			}
			if r.reloads != 0 {
				t.Fatalf("reloads = %d, want 0", r.reloads)
			}
			if _, err := os.Stat(filepath.Join(a.Root, "demo.conf")); !os.IsNotExist(err) {
				t.Fatalf("failed first-create candidate remains: %v", err)
			}
		})
	}
}

type fullValidationRunner struct {
	validations, reloads int
}

func (r *fullValidationRunner) Run(_ context.Context, prog string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	if prog == "/usr/sbin/php-fpm8.3" {
		if !reflect.DeepEqual(args, []string{"-t"}) {
			// Managed-only validation misses the bad global setting.
			return nil, nil, 0, nil
		}
		r.validations++
		return nil, []byte("invalid global FPM setting"), 1, nil
	}
	r.reloads++
	return nil, nil, 0, nil
}
