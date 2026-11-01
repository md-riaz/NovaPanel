package verbs_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	runner "novapanel/internal/exec"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/render/php"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

type matrixStore struct {
	*store.Store
	root, point string
}

func (s *matrixStore) CheckOperation(ctx context.Context, id string) error {
	err := s.Store.CheckOperation(ctx, id)
	if err != nil {
		return err
	}
	if s.point == "first" || s.point == "second" {
		index := 1
		if s.point == "second" {
			index = 3
		}
		paths := []string{"pool/demo.conf", "pool/demo.conf.lkg", "sites-available/demo.conf", "sites-available/demo.conf.lkg"}
		old, e1 := os.ReadFile(filepath.Join(s.root, fmt.Sprintf("old-%d", index)))
		now, e2 := os.ReadFile(filepath.Join(s.root, paths[index]))
		if e1 == nil && e2 == nil && !bytes.Equal(old, now) {
			os.Exit(42)
		}
	}
	return nil
}
func (s *matrixStore) CompleteOperation(ctx context.Context, id string, claim *store.JobClaim) error {
	if s.point == "beforecommit" {
		return errors.New("injected before commit")
	}
	err := s.Store.CompleteOperation(ctx, id, claim)
	if err != nil {
		return err
	}
	if s.point == "sql" {
		op, err := s.Store.LoadOperation(ctx, id)
		if err != nil || op.Identity.PublicationAudit == nil {
			panic("missing committed publication reservation")
		}
		for i, path := range []string{"pool/demo.conf", "pool/demo.conf.lkg", "sites-available/demo.conf", "sites-available/demo.conf.lkg"} {
			data, err := os.ReadFile(filepath.Join(s.root, path))
			if err != nil {
				panic(err)
			}
			if err := os.WriteFile(filepath.Join(s.root, fmt.Sprintf("desired-%d", i)), data, 0600); err != nil {
				panic(err)
			}
		}
		if err := os.WriteFile(filepath.Join(s.root, "final-id"), []byte(op.Identity.PublicationAudit.FinalEventID), 0600); err != nil {
			panic(err)
		}
		os.Exit(42)
	}
	if s.point == "aftercommit" {
		return errors.New("injected return after commit")
	}
	return nil
}

type matrixRunner struct{}

func (matrixRunner) Run(ctx context.Context, name string, args []string, opts runner.RunOpts) ([]byte, []byte, int, error) {
	role := os.Getenv("CRASH_ROLE")
	if role == "failfpm" && strings.Contains(name, "php-fpm") || role == "failnginx" && strings.Contains(name, "nginx") {
		return nil, nil, 1, errors.New("injected restoration service failure")
	}
	return nil, nil, 0, nil
}

// Exercise process death after each promotion and SQL commit, plus returned
// errors before and after commit; recovery failures must block startup.
func TestPHPQueueCrashIndependent(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("PHP recovery snapshot custody requires root")
	}
	mode := os.Getenv("CRASH_ROLE")
	if mode == "" {
		for _, point := range []string{"first", "second", "sql", "beforecommit", "aftercommit"} {
			t.Run(point, func(t *testing.T) {
				root := t.TempDir()
				for _, role := range func() []string {
					if point == "sql" || point == "beforecommit" || point == "aftercommit" {
						return []string{"crash", "recover"}
					}
					return []string{"crash", "failfpm", "failnginx", "recover"}
				}() {
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					out, stderr, exit, e := (runner.Real{}).Run(ctx, os.Args[0], []string{"-test.run=^TestPHPQueueCrashIndependent$", "-test.v"}, runner.RunOpts{Env: append(os.Environ(), "CRASH_ROLE="+role, "CRASH_ROOT="+root, "CRASH_POINT="+point)})
					cancel()
					t.Logf("%s: %s %s", role, out, stderr)
					if role == "crash" && (point == "first" || point == "second" || point == "sql") {
						if exit != 42 {
							t.Fatalf("expected injected os.Exit(42): exit=%d err=%v", exit, e)
						}
					} else if e != nil {
						t.Fatalf("child %s: %v", role, e)
					}
				}
			})
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := os.Getenv("CRASH_ROOT")
	if mode == "crash" {
		for _, d := range []string{"sites-available", "sites-enabled", "pool", "journal"} {
			if err := os.Mkdir(filepath.Join(root, d), 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	dbPath, auditPath := filepath.Join(root, "panel.db"), filepath.Join(root, "audit.log")
	open := func() (*store.Store, *audit.Log, *agent.Agent, *rpc.Server) {
		st, err := store.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		log, err := audit.Open(st, auditPath)
		if err != nil {
			t.Fatal(err)
		}
		a := agent.New()
		a.Store, a.Audit = st, log
		a.Nginx = &nginx.Applier{Root: root, Runner: matrixRunner{}, Probe: func(context.Context, nginx.Desired) error { return nil }}
		a.PHP = &agent.PHPDependencies{Pools: &php.Applier{Root: filepath.Join(root, "pool"), Runner: matrixRunner{}, Probe: func(context.Context, php.Desired) error { return nil }}, RecoveryRoot: filepath.Join(root, "journal"), ResolveIdentity: func(context.Context, store.Site) (uint32, uint32, error) { return 1001, 1001, nil }}
		server := rpc.NewServer(a)
		server.ConfigureJobs()
		if mode == "crash" {
			a.Jobs = jobs.NewQueue(&matrixStore{Store: st, root: root, point: os.Getenv("CRASH_POINT")}, nil, log)
			a.Jobs.SetMutationAdmission(a.CheckMutationAdmission)
			a.Jobs.SetRunner(server.ExecuteJob)
		}
		return st, log, a, server
	}
	st, log, a, server := open()
	if mode == "failfpm" || mode == "failnginx" {
		defer st.Close()
		defer log.Close()
		if err := a.PrepareStartup(ctx); err == nil {
			t.Fatal("failed restore must block startup")
		}
		var done, forged int
		if err := st.Tx(ctx, func(tx *sql.Tx) error {
			if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE verb='php.set' AND state='done'`).Scan(&done); e != nil {
				return e
			}
			return tx.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE verb='php.set' AND json_extract(detail_json,'$.publication')='committed'`).Scan(&forged)
		}); err != nil || done != 0 || forged != 0 {
			t.Fatalf("done=%d forged=%d err=%v", done, forged, err)
		}
		return
	}
	paths := []string{"pool/demo.conf", "pool/demo.conf.lkg", "sites-available/demo.conf", "sites-available/demo.conf.lkg"}
	if mode == "crash" {
		old := nginx.Desired{Name: "demo", Domains: []string{"old.example.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo", PHP: &php.Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001}}
		if err := a.Nginx.ApplyPHP(ctx, a.PHP.Pools, old, func(context.Context) error { return nil }, func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
		for i, p := range paths {
			b, e := os.ReadFile(filepath.Join(root, p))
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(root, fmt.Sprintf("old-%d", i)), b, 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
	if mode == "recover" {
		defer func() {
			for i, p := range paths {
				got, e := os.ReadFile(filepath.Join(root, p))
				if e != nil {
					t.Fatal(e)
				}
				old, e := os.ReadFile(filepath.Join(root, fmt.Sprintf("old-%d", i)))
				if e != nil {
					t.Fatal(e)
				}
				if (os.Getenv("CRASH_POINT") != "sql" && os.Getenv("CRASH_POINT") != "aftercommit") && !bytes.Equal(got, old) {
					t.Fatalf("%s not restored", p)
				}
				if os.Getenv("CRASH_POINT") == "sql" {
					want, err := os.ReadFile(filepath.Join(root, fmt.Sprintf("desired-%d", i)))
					if err != nil || !bytes.Equal(got, want) {
						t.Fatalf("%s changed after committed crash: %v", p, err)
					}
				}
				if (os.Getenv("CRASH_POINT") == "sql" || os.Getenv("CRASH_POINT") == "aftercommit") && bytes.Equal(got, old) {
					t.Fatalf("%s not committed", p)
				}
			}
		}()
	}
	if mode == "recover" {
		defer st.Close()
		defer log.Close()
		if err := a.PrepareStartup(ctx); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("CRASH_POINT") == "sql" || os.Getenv("CRASH_POINT") == "aftercommit" {
			limits, err := st.GetPHPLimits(ctx, "01K8WMXZF57CGNZKX1FZ38DZMA")
			if err != nil || limits.MemoryMB != 512 {
				t.Fatalf("committed limits=%+v err=%v", limits, err)
			}
			var eid string
			var count int
			e := st.Tx(ctx, func(tx *sql.Tx) error {
				return tx.QueryRowContext(ctx, `SELECT id FROM events WHERE json_extract(detail_json,'$.publication')='committed' AND verb='php.set'`).Scan(&eid)
			})
			if e != nil {
				t.Fatal(e)
			}
			e = st.Tx(ctx, func(tx *sql.Tx) error {
				return tx.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE json_extract(detail_json,'$.publication')='committed' AND verb='php.set'`).Scan(&count)
			})
			if e != nil {
				t.Fatal(e)
			}
			data, readErr := os.ReadFile(auditPath)
			old, e := os.ReadFile(filepath.Join(root, "final-id"))
			if e != nil || string(old) != eid {
				t.Fatalf("final ID changed: old=%s new=%s err=%v", old, eid, e)
			}
			if e != nil || readErr != nil || count != 1 || strings.Count(string(data), eid) != 1 {
				t.Fatalf("audit id=%s count=%d file=%d err=%v %v", eid, count, strings.Count(string(data), eid), e, readErr)
			}
		}
		entries, err := os.ReadDir(filepath.Join(root, "journal"))
		if err != nil {
			t.Fatal(err)
		}
		_ = entries
		var count int
		err = st.Tx(ctx, func(tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE verb='php.set' AND state=?`, map[bool]string{true: "done", false: "failed"}[(os.Getenv("CRASH_POINT") == "sql" || os.Getenv("CRASH_POINT") == "aftercommit")]).Scan(&count)
		})
		if err != nil || count != 1 {
			t.Fatalf("state count=%d err=%v", count, err)
		}
		pool, e := os.ReadFile(filepath.Join(root, "pool", "demo.conf"))
		if os.Getenv("CRASH_POINT") == "sql" || os.Getenv("CRASH_POINT") == "aftercommit" {
			if e != nil || !strings.Contains(string(pool), "memory_limit] = 512M") {
				t.Fatalf("committed pool: %v", e)
			}
		} else if e != nil {
			t.Fatalf("original pool missing: %v", e)
		}
		return
	}
	if err := a.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	const sid = "01K8WMXZF57CGNZKX1FZ38DZMA"
	version := "8.3"
	if _, err := st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "php", Docroot: "/srv/sites/demo/public", PHPVersion: &version}, store.Domain{ID: "01K8WMXZF57CGNZKX1FZ38DZMB", FQDN: "demo.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	reply := server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Kind: "user", Role: "admin", Session: "admin-session", UserID: "admin-user"}), rpc.Request{Verb: "php.set", Args: json.RawMessage(fmt.Sprintf(`{"site_id":%q,"version":"8.3","limits":{"memory_mb":512}}`, sid))})
	if !reply.OK {
		t.Fatalf("dispatch: %+v", reply)
	}
	job := reply.Data.(jobs.EnqueueResult).Job
	_ = job
	err := a.Jobs.Run(ctx)
	if os.Getenv("CRASH_POINT") == "aftercommit" || os.Getenv("CRASH_POINT") == "beforecommit" {
		if err == nil {
			t.Fatal("expected injected commit error")
		}
		op, e := st.LoadOperation(ctx, job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if (op.Checkpoint == "done") != (os.Getenv("CRASH_POINT") == "aftercommit") {
			t.Fatalf("readback checkpoint=%s", op.Checkpoint)
		}
		if os.Getenv("CRASH_POINT") == "aftercommit" {
			if op.Identity.PublicationAudit == nil {
				t.Fatal("missing audit reservation")
			}
			if e = os.WriteFile(filepath.Join(root, "final-id"), []byte(op.Identity.PublicationAudit.FinalEventID), 0600); e != nil {
				t.Fatal(e)
			}
		}
		if e = log.Close(); e != nil {
			t.Fatal(e)
		}
		if e = st.Close(); e != nil {
			t.Fatal(e)
		}
		return
	}
	t.Fatal("did not crash")
}
