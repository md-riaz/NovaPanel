package verbs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestReconcilePHPOrphansAndCommittedPool(t *testing.T) {
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
	memory := 128
	if err = st.SetSitePHP(ctx, sid, version, store.PHPLimits{MemoryMB: &memory}); err != nil {
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
	if err = os.WriteFile(filepath.Join(dir, "demo.conf"), []byte("uncommitted"), 0644); err != nil {
		t.Fatal(err)
	}
	r := &reconcileAccounts{root: root, accounts: map[string]string{"demo": "novapanel-site=" + sid, "orphan": "novapanel-site=" + orphan, "pending": "novapanel-site=" + pending, "retained": "novapanel-retained=" + retained, "foreign": ""}}
	a := agent.New()
	a.Store = st
	a.Jobs = jobs.NewQueue(st, nil, nil)
	a.Exec = r
	a.Cfg.SitesRoot = root
	a.PHP = &php.PoolApplier{Dir: dir, Runner: r, Probe: func(context.Context) error { return nil }}
	if _, err = reconcilePHP(ctx, a.Store, a.Jobs, a.Exec, a.PHP, a.Cfg.SitesRoot); err != nil {
		t.Fatal(err)
	}
	if len(r.deleted) != 1 || r.deleted[0] != "orphan" {
		t.Fatalf("deleted %v", r.deleted)
	}
	b, err := os.ReadFile(filepath.Join(dir, "demo.conf"))
	if err != nil || !strings.Contains(string(b), "memory_limit] = 128M") {
		t.Fatalf("pool %q: %v", b, err)
	}
}
