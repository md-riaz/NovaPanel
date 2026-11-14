package verbs

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/store"
)

func TestRecoverCommittedPHPRespectsLifecycleLock(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sid, _ := id.New()
	did, _ := id.New()
	version := "8.3"
	if _, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "php", PHPVersion: &version, Docroot: filepath.Join(root, "demo/public")}, store.Domain{ID: did, FQDN: "demo.test"}); err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	q := jobs.NewQueue(st, nil, nil)
	unlock, err := q.LockSiteOSUser(ctx, sid, "demo")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	r := &reconcileAccounts{root: root, accounts: map[string]string{"demo": "novapanel-site=" + sid}}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if err = RecoverCommittedPHP(bounded, st, q, r, &php.PoolApplier{Dir: root, Runner: r}, root); err == nil {
		t.Fatal("recovery bypassed lifecycle lock")
	}
}
