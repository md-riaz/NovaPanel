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

type orderedOrphanRunner struct {
	*orphanPoolRunner
	order []string
}

func (r *orderedOrphanRunner) Run(ctx context.Context, prog string, args []string, opts runner.RunOpts) ([]byte, []byte, int, error) {
	if prog == "/usr/bin/getent" && len(args) == 1 {
		var b strings.Builder
		for _, name := range r.order {
			b.WriteString(name + ":x:1001:1001:" + r.accounts[name] + ":" + filepath.Join(r.root, name) + ":/usr/sbin/nologin\n")
		}
		return []byte(b.String()), nil, 0, nil
	}
	return r.orphanPoolRunner.Run(ctx, prog, args, opts)
}

func TestReconcileOrphanContinuesAndRetries(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sites, logs, pools := filepath.Join(root, "sites"), filepath.Join(root, "logs"), filepath.Join(root, "pools")
	for _, dir := range []string{sites, logs, pools} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "php-fpm.conf"), []byte("[global]\ninclude = "+filepath.Join(pools, "*.conf")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	accounts := map[string]string{}
	for _, name := range []string{"foreign", "first", "second"} {
		sid, _ := id.New()
		accounts[name] = "novapanel-site=" + sid
		owner := sid
		if name == "foreign" {
			owner, _ = id.New()
		}
		if err := os.WriteFile(filepath.Join(pools, "novapanel-"+name+".conf"), []byte("; novapanel-managed site="+owner+"\n["+name+"]\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	base := &reconcileAccounts{root: sites, accounts: accounts}
	r := &orderedOrphanRunner{orphanPoolRunner: &orphanPoolRunner{reconcileAccounts: base, fail: true}, order: []string{"foreign", "first", "second"}}
	pool := &php.PoolApplier{Dir: pools, Runner: r, Probe: func(context.Context) error { return nil }}
	q := jobs.NewQueue(st, nil, nil)
	_, err = reconcilePHP(ctx, st, q, r, pool, sites, logs)
	if err == nil || !errors.Is(err, jobs.ErrRetry) || !strings.Contains(err.Error(), "foreign") || !strings.Contains(err.Error(), "first") || !strings.Contains(err.Error(), "second") {
		t.Fatalf("expected all account errors including retry: %v", err)
	}
	for _, name := range []string{"first", "second"} {
		if _, e := os.Stat(filepath.Join(pools, "novapanel-"+name+".conf")); !os.IsNotExist(e) {
			t.Errorf("owned pool %s remains: %v", name, e)
		}
	}
	if _, e := os.Stat(filepath.Join(pools, "novapanel-foreign.conf")); e != nil {
		t.Errorf("foreign pool removed: %v", e)
	}
	_, err = reconcilePHP(ctx, st, q, r, pool, sites, logs)
	if err == nil || !strings.Contains(err.Error(), "foreign") || !strings.Contains(err.Error(), "first") || !strings.Contains(err.Error(), "second") {
		t.Fatalf("retry did not revisit retained accounts: %v", err)
	}
	if len(base.deleted) != 0 {
		t.Fatalf("unsafe account removal: %v", base.deleted)
	}
}
