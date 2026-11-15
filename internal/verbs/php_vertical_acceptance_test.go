package verbs_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	runner "novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

type phpScratchRunner struct{}

func (phpScratchRunner) Run(_ context.Context, bin string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	if bin == "/usr/bin/getent" {
		return []byte("demo:x:1001:1001:novapanel-site=01K8WMXZF57CGNZKX1FZ38DZMA:/srv/sites/demo:/usr/sbin/nologin\n"), nil, 0, nil
	}
	if bin == "/usr/bin/systemctl" && len(args) > 0 && args[0] == "show" {
		return []byte("loaded\n"), nil, 0, nil
	}
	return nil, nil, 0, nil
}
func requireRootPHPCustody(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
}
func TestPHPSetQueuePublishesScratchConfiguration(t *testing.T) {
	requireRootPHPCustody(t)
	ctx := context.Background()
	root := t.TempDir()
	pool := filepath.Join(root, "pool")
	if err := os.Mkdir(pool, 0700); err != nil {
		t.Fatal(err)
	}
	master := "[global]\npid = /run/php/php8.3-fpm.pid\nerror_log = /var/log/php8.3-fpm.log\ninclude = " + filepath.Join(pool, "*.conf") + "\n"
	if err := os.WriteFile(filepath.Join(root, "php-fpm.conf"), []byte(master), 0600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log, err := audit.Open(st, filepath.Join(root, "audit"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	a := agent.New()
	a.Store = st
	a.Audit = log
	a.Exec = phpScratchRunner{}
	a.PHP = &php.PoolApplier{Dir: pool, Runner: a.Exec, Probe: func(context.Context) error { return nil }}
	a.Nginx = &nginx.Applier{Root: root, Runner: a.Exec, Probe: func(context.Context, nginx.Desired) error { return nil }}
	server := rpc.NewServer(a)
	server.ConfigureJobs()
	if err = a.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	const sid = "01K8WMXZF57CGNZKX1FZ38DZMA"
	version := "8.3"
	_, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "php", Docroot: "/srv/sites/demo/public", PHPVersion: &version}, store.Domain{ID: "01K8WMXZF57CGNZKX1FZ38DZMB", FQDN: "demo.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	reply := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "php.set", Args: json.RawMessage(fmt.Sprintf(`{"site_id":%q,"version":"8.3","limits":{"memory_mb":512}}`, sid))})
	if !reply.OK {
		t.Fatal(reply)
	}
	job := reply.Data.(jobs.EnqueueResult).Job
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(run) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		j, e := st.GetJob(ctx, job.ID)
		if e == nil && j.State == "done" {
			break
		}
		if e != nil && !errors.Is(e, store.ErrPublicationPending) {
			t.Fatal(e)
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("job %+v %v", j, e)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(pool, "novapanel-demo.conf"))
	if err != nil || !strings.Contains(string(b), "memory_limit] = 512M") {
		t.Fatalf("pool %s %v", b, err)
	}
	limits, err := st.SitePHPLimits(ctx, sid)
	if err != nil || limits.MaxChildren == nil || *limits.MaxChildren < 2 || *limits.MaxChildren > 32 {
		t.Fatalf("settings %+v %v", limits, err)
	}
}
