package verbs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"novapanel/internal/agent"
	runner "novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/store"
)

type reconcileAccounts struct {
	root     string
	accounts map[string]string
	deleted  []string
}

func (r *reconcileAccounts) Run(_ context.Context, prog string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	if prog == "/usr/bin/getent" {
		var b strings.Builder
		for name, marker := range r.accounts {
			if len(args) == 1 || args[1] == name {
				b.WriteString(name + ":x:1001:1001:" + marker + ":" + filepath.Join(r.root, name) + ":/usr/sbin/nologin\n")
			}
		}
		if b.Len() == 0 && len(args) > 1 {
			return nil, nil, 2, nil
		}
		return []byte(b.String()), nil, 0, nil
	}
	if prog == "/usr/sbin/userdel" {
		r.deleted = append(r.deleted, args[len(args)-1])
	}
	return nil, nil, 0, nil
}
func TestReconcileOrphanResourceGuard(t *testing.T) {
	for _, kind := range []string{"home", "log", "pool", "dangling", "unknown", "absent", "locks"} {
		t.Run(kind, func(t *testing.T) {
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
			sid, _ := id.New()
			r := &reconcileAccounts{root: sites, accounts: map[string]string{"orphan": "novapanel-site=" + sid}}
			q := jobs.NewQueue(st, nil, nil)
			pool := &php.PoolApplier{Dir: pools, Runner: r}
			switch kind {
			case "home":
				err = os.Mkdir(filepath.Join(sites, "orphan"), 0755)
			case "log":
				err = os.Mkdir(filepath.Join(logs, "orphan"), 0755)
			case "pool":
				err = os.WriteFile(filepath.Join(pools, "novapanel-orphan.conf"), []byte("pool"), 0644)
			case "dangling":
				err = os.Symlink("missing", filepath.Join(sites, "orphan"))
			case "unknown":
				logs = filepath.Join(root, "missing")
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind == "locks" {
				unlock, e := q.LockSiteOSUser(ctx, sid, "orphan")
				if e != nil {
					t.Fatal(e)
				}
				blocked, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
				_, e = reconcilePHP(blocked, st, q, r, pool, sites, logs)
				cancel()
				unlock()
				if e == nil || len(r.deleted) != 0 {
					t.Fatalf("lock not respected: %v %v", e, r.deleted)
				}
			}
			changed, err := reconcilePHP(ctx, st, q, r, pool, sites, logs)
			if err == nil || len(r.deleted) != 0 || len(changed) != 0 {
				t.Fatalf("orphan must remain for operator: deleted=%v changed=%v err=%v", r.deleted, changed, err)
			}
		})
	}
}

func TestReconcileMalformedMarkerRequiresOperator(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	r := &reconcileAccounts{root: root, accounts: map[string]string{"orphan": "novapanel-site=invalid"}}
	_, err = reconcilePHP(ctx, st, jobs.NewQueue(st, nil, nil), r, &php.PoolApplier{Dir: root, Runner: r}, root, root)
	if err == nil || !strings.Contains(err.Error(), "manual cleanup") || len(r.deleted) != 0 {
		t.Fatalf("malformed marker must be retained for operator: deleted=%v err=%v", r.deleted, err)
	}
}

func TestSiteJobLiveFailsClosedOnMissingIdentity(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	jobID, _ := id.New()
	if err = st.CreateJob(ctx, store.Job{ID: jobID, Verb: "site.delete", ObjectKey: "site:", Args: json.RawMessage(`{}`), State: "queued"}); err != nil {
		t.Fatal(err)
	}
	sid, _ := id.New()
	live, err := siteJobLive(ctx, st, sid, "orphan")
	if err != nil || !live {
		t.Fatalf("missing job identity must block cleanup: live=%v err=%v", live, err)
	}
}

func TestStartupPHPRecoveryPropagatesStoreFailure(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil { t.Fatal(err) }
	q := jobs.NewQueue(st, nil, nil)
	if err := st.Close(); err != nil { t.Fatal(err) }
	r := &reconcileAccounts{accounts: map[string]string{}}
	if err := RecoverCommittedPHP(ctx, st, q, r, &php.PoolApplier{Runner: r}, t.TempDir()); err == nil {
		t.Fatal("startup recovery ignored store failure")
	}
}

func TestReconcilePHPOrphansAndCommittedPool(t *testing.T) {
	testReconcilePHPOrphansAndPool(t, false)
}

func TestReconcilePHPBackfillsLegacyCap(t *testing.T) {
	testReconcilePHPOrphansAndPool(t, true)
}

func testReconcilePHPOrphansAndPool(t *testing.T, legacy bool) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sid, _ := id.New()
	did, _ := id.New()
	orphan, _ := id.New()
	pending, _ := id.New()
	retained, _ := id.New()
	version := "8.3"
	_, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "php", PHPVersion: &version, Docroot: filepath.Join(root, "demo/public")}, store.Domain{ID: did, FQDN: "demo.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	memory, cap := 128, 5
	limits := store.PHPLimits{MemoryMB: &memory, MaxChildren: &cap}
	if legacy {
		limits.MaxChildren = nil
	}
	if err = st.SetSitePHP(ctx, sid, version, limits); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"site_id": pending, "name": "pending"})
	jobID, _ := id.New()
	if err = st.CreateJob(ctx, store.Job{ID: jobID, Verb: "site.create", ObjectKey: "site:pending", Args: raw, State: "queued"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "pools")
	if err = os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	master := "[global]\npid = /run/php/php8.3-fpm.pid\nerror_log = /var/log/php8.3-fpm.log\ninclude = " + filepath.Join(dir, "*.conf") + "\n"
	if err = os.WriteFile(filepath.Join(root, "php-fpm.conf"), []byte(master), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "novapanel-demo.conf"), []byte("; novapanel-managed site="+sid+"\nuncommitted"), 0644); err != nil {
		t.Fatal(err)
	}
	r := &reconcileAccounts{root: root, accounts: map[string]string{"demo": "novapanel-site=" + sid, "orphan": "novapanel-site=" + orphan, "pending": "novapanel-site=" + pending, "retained": "novapanel-retained=" + retained, "foreign": ""}}
	a := agent.New()
	a.Store = st
	a.Jobs = jobs.NewQueue(st, nil, nil)
	a.Exec = r
	a.Cfg.SitesRoot = root
	a.PHP = &php.PoolApplier{Dir: dir, Runner: r, Probe: func(context.Context) error { return nil }}
	// A queued mutation must not suppress startup projection recovery: no worker
	// is running yet, and it will replay after recovery.
	queuedID, _ := id.New()
	if err = st.CreateJob(ctx, store.Job{ID: queuedID, Verb: "php.set", ObjectKey: "site:" + sid, Args: json.RawMessage(`{"site_id":"` + sid + `"}`), State: "queued"}); err != nil {
		t.Fatal(err)
	}
	unlock, err := a.Jobs.LockSiteOSUser(ctx, sid, "demo")
	if err != nil { t.Fatal(err) }
	blocked, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	if err = RecoverCommittedPHP(blocked, st, a.Jobs, a.Exec, a.PHP, root); err == nil { t.Fatal("startup recovery ignored site lock") }
	cancel()
	unlock()
	if err = RecoverCommittedPHP(ctx, st, a.Jobs, a.Exec, a.PHP, root); err != nil { t.Fatal(err) }
	b, err := os.ReadFile(filepath.Join(dir, "novapanel-demo.conf"))
	if err != nil || !strings.Contains(string(b), "memory_limit] = 128M") { t.Fatalf("startup pool %q: %v", b, err) }
	logs := filepath.Join(root, "logs")
	if err = os.Mkdir(logs, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = reconcilePHP(ctx, a.Store, a.Jobs, a.Exec, a.PHP, a.Cfg.SitesRoot, logs); err == nil {
		t.Fatal("expected orphan to require manual cleanup")
	}
	if len(r.deleted) != 0 {
		t.Fatalf("deleted %v", r.deleted)
	}
	persisted, err := st.SitePHPLimits(ctx, sid)
	if err != nil || persisted.MaxChildren == nil || *persisted.MaxChildren < 2 || persisted.MemoryMB == nil || *persisted.MemoryMB != memory {
		t.Fatalf("persisted limits %+v: %v", persisted, err)
	}
	b, err = os.ReadFile(filepath.Join(dir, "novapanel-demo.conf"))
	if err != nil || !strings.Contains(string(b), "memory_limit] = 128M") {
		t.Fatalf("pool %q: %v", b, err)
	}
}
