package verbs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	runner "novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"testing"
)

type purgeRunner struct {
	root, id         string
	missing, foreign bool
}

func (r *purgeRunner) Run(_ context.Context, prog string, _ []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	switch prog {
	case "/usr/bin/getent":
		if r.missing {
			return nil, nil, 2, nil
		}
		marker := "novapanel-site=" + r.id
		if r.foreign {
			marker = "foreign"
		}
		return []byte(fmt.Sprintf("demo:x:1001:1001:%s:%s:/usr/sbin/nologin\n", marker, filepath.Join(r.root, "demo"))), nil, 0, nil
	case "/usr/sbin/userdel":
		r.missing = true
	}
	return nil, nil, 0, nil
}

func TestPHPSitePurgeCrashReplay(t *testing.T) {
	for _, variant := range []string{"replay", "foreign-home", "foreign-account"} {
		t.Run(variant, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
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
			a := agent.New()
			a.Store = st
			a.Audit = log
			a.Cfg.SitesRoot = filepath.Join(root, "sites")
			a.Cfg.TLSDir = filepath.Join(root, "tls")
			for _, p := range []string{filepath.Join(a.Cfg.SitesRoot, "demo", "public"), filepath.Join(root, "nginx", "sites-available"), filepath.Join(root, "nginx", "sites-enabled"), filepath.Join(root, "pool")} {
				if err = os.MkdirAll(p, 0755); err != nil {
					t.Fatal(err)
				}
			}
			sid, _ := id.New()
			did, _ := id.New()
			r := &purgeRunner{root: a.Cfg.SitesRoot, id: sid}
			a.Exec = r
			a.Nginx = &nginx.Applier{Root: filepath.Join(root, "nginx"), Runner: r, Probe: func(context.Context, nginx.Desired) error { return nil }}
			a.PHP = &php.PoolApplier{Dir: filepath.Join(root, "pool"), Runner: r, Probe: func(context.Context) error { return nil }}
			if err = os.WriteFile(filepath.Join(root, "pool", "demo.conf"), []byte("pool"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "php", Docroot: filepath.Join(a.Cfg.SitesRoot, "demo", "public")}, store.Domain{ID: did, FQDN: "demo.test"}); err != nil {
				t.Fatal(err)
			}
			server := rpc.NewServer(a)
			server.ConfigureJobs()
			reply := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "site.delete", Args: json.RawMessage(`{"site_id":"` + sid + `","purge_files":true}`)})
			if !reply.OK {
				t.Fatal(reply)
			}
			j := jobs.Job(reply.Data.(jobs.EnqueueResult).Job)
			if err = st.Tx(ctx, func(tx *sql.Tx) error {
				_, e := tx.ExecContext(ctx, `CREATE TRIGGER fail_delete BEFORE DELETE ON sites BEGIN SELECT RAISE(FAIL, 'injected'); END`)
				return e
			}); err != nil {
				t.Fatal(err)
			}
			if err = server.ExecuteJob(ctx, &j); !errors.Is(err, jobs.ErrRetry) || !r.missing {
				t.Fatalf("expected failure after userdel: %v missing=%v", err, r.missing)
			}
			if err = st.Tx(ctx, func(tx *sql.Tx) error { _, e := tx.ExecContext(ctx, `DROP TRIGGER fail_delete`); return e }); err != nil {
				t.Fatal(err)
			}
			if variant == "foreign-home" {
				if err = os.Mkdir(filepath.Join(a.Cfg.SitesRoot, "demo"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if variant == "foreign-account" {
				r.missing = false
				r.foreign = true
			}
			j, err = st.GetJob(ctx, j.ID)
			if err != nil {
				t.Fatal(err)
			}
			err = server.ExecuteJob(ctx, &j)
			if variant == "replay" {
				if err != nil {
					t.Fatalf("replay: %v", err)
				}
				if _, err = st.SiteByID(ctx, sid); err != sql.ErrNoRows {
					t.Fatalf("row remains: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("unproven resource accepted")
				}
				if _, err = st.SiteByID(ctx, sid); err != nil {
					t.Fatalf("row removed: %v", err)
				}
				if variant == "foreign-home" {
					if _, err = os.Stat(filepath.Join(a.Cfg.SitesRoot, "demo")); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}
