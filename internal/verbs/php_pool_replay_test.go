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

type poolReplayRunner struct{ root, id string }

func (r poolReplayRunner) Run(_ context.Context, prog string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	if prog == "/usr/bin/getent" {
		return []byte("demo:x:1001:1001:novapanel-site=" + r.id + ":" + filepath.Join(r.root, "demo") + ":/usr/sbin/nologin\n"), nil, 0, nil
	}
	if prog == "/usr/bin/systemctl" && len(args) > 0 && args[0] == "show" {
		return []byte("loaded\n"), nil, 0, nil
	}
	return nil, nil, 0, nil
}

func TestPHPSetReplayUsesCommittedLimits(t *testing.T) {
	testPHPSetReplay(t, false)
}

func TestPHPSetReplayBackfillsLegacyCap(t *testing.T) {
	testPHPSetReplay(t, true)
}

func testPHPSetReplay(t *testing.T, legacy bool) {
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
	_, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "php", PHPVersion: &version, Docroot: filepath.Join(root, "sites", "demo", "public")}, store.Domain{ID: did, FQDN: "demo.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	memory, cap := 128, 5
	limitsInitial := store.PHPLimits{MemoryMB: &memory, MaxChildren: &cap}
	if legacy {
		limitsInitial.MaxChildren = nil
	}
	if err = st.SetSitePHP(ctx, sid, version, limitsInitial); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "pool")
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
	r := poolReplayRunner{filepath.Join(root, "sites"), sid}
	a := agent.New()
	a.Store = st
	a.Cfg.SitesRoot = r.root
	a.Exec = r
	a.PHP = &php.PoolApplier{Dir: dir, Runner: r, Probe: func(context.Context) error { return nil }}
	a.Jobs = jobs.NewQueue(st, nil, nil)
	upload := 32
	raw, _ := json.Marshal(phpSetArgs{SiteID: sid, Version: version, Limits: store.PHPLimits{UploadMB: &upload}})
	if err = phpSetExec(ctx, a, &jobs.Job{Args: raw, Attempts: 2}); err != nil {
		t.Fatal(err)
	}
	limits, err := st.SitePHPLimits(ctx, sid)
	if err != nil || limits.MaxChildren == nil || *limits.MaxChildren < 2 || limits.MemoryMB == nil || *limits.MemoryMB != 128 || limits.UploadMB == nil || *limits.UploadMB != 32 {
		t.Fatalf("limits %+v: %v", limits, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "novapanel-demo.conf"))
	if err != nil || !strings.Contains(string(b), "memory_limit] = 128M") || !strings.Contains(string(b), "upload_max_filesize] = 32M") {
		t.Fatalf("pool %q: %v", b, err)
	}
}
