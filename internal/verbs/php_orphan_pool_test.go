package verbs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	runner "novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/store"
)

type orphanPoolRunner struct {
	*reconcileAccounts
	commands []string
	fail     bool
}

func (r *orphanPoolRunner) Run(ctx context.Context, prog string, args []string, opts runner.RunOpts) ([]byte, []byte, int, error) {
	if prog == "/usr/bin/systemctl" {
		r.commands = append(r.commands, strings.Join(args, " "))
		if r.fail {
			r.fail = false
			return nil, nil, 1, errors.New("service failure")
		}
	}
	return r.reconcileAccounts.Run(ctx, prog, args, opts)
}

func TestReconcileOrphanPoolOwnershipAndRetry(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		foreign, other, fail bool
	}{
		{name: "last pool"}, {name: "other pool", other: true}, {name: "foreign collision", foreign: true}, {name: "service retry", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			st, err := store.Open(filepath.Join(root, "panel.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			sites, logs, pools := filepath.Join(root, "sites"), filepath.Join(root, "logs"), filepath.Join(root, "pools")
			for _, d := range []string{sites, logs, pools} {
				if err := os.Mkdir(d, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "php-fpm.conf"), []byte("[global]\ninclude = "+filepath.Join(pools, "*.conf")+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			sid, _ := id.New()
			owner := sid
			if tc.foreign {
				owner, _ = id.New()
			}
			path := filepath.Join(pools, "novapanel-orphan.conf")
			original := []byte("; novapanel-managed site=" + owner + "\n[orphan]\n")
			if err := os.WriteFile(path, original, 0644); err != nil {
				t.Fatal(err)
			}
			if tc.other {
				if err := os.WriteFile(filepath.Join(pools, "other.conf"), []byte("[other]\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			r := &orphanPoolRunner{reconcileAccounts: &reconcileAccounts{root: sites, accounts: map[string]string{"orphan": "novapanel-site=" + sid}}, fail: tc.fail}
			pool := &php.PoolApplier{Dir: pools, Runner: r, Probe: func(context.Context) error { return nil }}
			q := jobs.NewQueue(st, nil, nil)
			_, err = reconcilePHP(ctx, st, q, r, pool, sites, logs)
			if err == nil {
				t.Fatal("orphan account must remain for operator")
			}
			if tc.foreign {
				b, e := os.ReadFile(path)
				if e != nil || string(b) != string(original) || len(r.commands) != 0 {
					t.Fatalf("foreign pool changed: %q %v commands %v", b, e, r.commands)
				}
				return
			}
			if _, e := os.Stat(path); !os.IsNotExist(e) {
				t.Fatalf("owned pool remains: %v", e)
			}
			if tc.fail {
				if !errors.Is(err, jobs.ErrRetry) {
					t.Fatalf("service failure must retry: %v", err)
				}
				_, err = reconcilePHP(ctx, st, q, r, pool, sites, logs)
				if err == nil {
					t.Fatal("UID guard must remain")
				}
			}
			want := "stop php8.3-fpm.service"
			if tc.other {
				want = "enable --now php8.3-fpm.service"
			}
			if !strings.Contains(strings.Join(r.commands, "|"), want) {
				t.Fatalf("missing service operation %s: %v", want, r.commands)
			}
			if len(r.deleted) != 0 {
				t.Fatalf("deleted account: %v", r.deleted)
			}
		})
	}
}
