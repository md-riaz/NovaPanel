package verbs_test

import (
	"context"
	"encoding/json"
	"io"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type sitePurgeRunner struct {
	db, user, site, root string
	statements           []string
}

func (r *sitePurgeRunner) Run(ctx context.Context, bin string, args []string, o exec.RunOpts) ([]byte, []byte, int, error) {
	if bin == "/usr/bin/getent" {
		return []byte("demo:x:1001:1001:novapanel-site=" + r.site + ":" + filepath.Join(r.root, "demo") + ":/usr/sbin/nologin\n"), nil, 0, nil
	}
	if bin == "/usr/sbin/usermod" || bin == "/usr/bin/systemctl" || bin == "/usr/sbin/nginx" {
		return nil, nil, 0, nil
	}
	if bin == "/usr/bin/mariadb" {
		var text string
		if o.Stdin != nil {
			b, _ := io.ReadAll(o.Stdin)
			text = string(b)
		}
		r.statements = append(r.statements, text)
		if strings.HasPrefix(text, "SELECT COALESCE((SELECT phase") {
			return []byte("created\n"), nil, 0, nil
		}
		if strings.HasPrefix(text, "SELECT") {
			return []byte("1\n"), nil, 0, nil
		}
		return nil, nil, 0, nil
	}
	return nil, nil, 0, nil
}
func TestSiteDeletePurgeDropsManagedDatabaseAndUser(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for _, d := range []string{"sites-available", "sites-enabled", "tls"} {
		if err := os.Mkdir(filepath.Join(root, d), 0700); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log, err := audit.Open(st, filepath.Join(root, "audit"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	sid := "01K8WMXZF57CGNZKX1FZ38DZMA"
	db := "01K8WMXZF57CGNZKX1FZ38DZMQ"
	uid := "01K8WMXZF57CGNZKX1FZ38DZMR"
	sites := filepath.Join(root, "sites")
	_, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: filepath.Join(sites, "demo", "public")}, store.Domain{ID: "01K8WMXZF57CGNZKX1FZ38DZMB", FQDN: "demo.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	if err = st.CreateDatabasePrimary(ctx, store.DatabasePrimary{ID: db, UserID: uid, SiteID: sid}); err != nil {
		t.Fatal(err)
	}
	runner := &sitePurgeRunner{db: db, user: uid, site: sid, root: sites}
	a := agent.New()
	a.Cfg.DBPath = filepath.Join(root, "panel.db")
	a.SSHD.Root = filepath.Join(root, "missing-ssh")
	a.Store = st
	a.Audit = log
	a.Exec = runner
	a.Cfg.SitesRoot = sites
	a.Cfg.TLSDir = filepath.Join(root, "tls")
	a.Nginx = &nginx.Applier{Root: root, Runner: runner, Probe: func(context.Context, nginx.Desired) error { return nil }}
	server := rpc.NewServer(a)
	server.ConfigureJobs()
	if err = a.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	reply := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "site.delete", Args: json.RawMessage(`{"site_id":"` + sid + `","purge_databases":true}`)})
	if !reply.OK {
		t.Fatalf("admission: %+v", reply)
	}
	job := reply.Data.(jobs.EnqueueResult).Job
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(run) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		j, e := st.GetJob(ctx, job.ID)
		if e == nil && j.State == "done" {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			workerErr := <-done
			t.Logf("worker error: %v", workerErr)
			op, oe := st.LoadOperation(ctx, job.ID)
			t.Fatalf("job %+v err %v op %+v opErr %v statements %v", j, e, op, oe, runner.statements)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	statements := strings.Join(runner.statements, "\n")
	if !strings.Contains(statements, "DROP USER IF EXISTS") || !strings.Contains(statements, "DROP DATABASE IF EXISTS") {
		t.Fatalf("missing managed drops: %s", statements)
	}
	if _, err = st.SiteByID(ctx, sid); err == nil {
		t.Fatal("site remains")
	}
	if _, err = st.GetDatabase(ctx, db); err == nil {
		t.Fatal("database remains")
	}
}
