package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	certs "novapanel/internal/tls"
)

func TestDomainAddImmediatelyQueuesFullSANExpansion(t *testing.T) {
	testDomainExpansionWorker(t, false)
}

func TestDomainAddExpansionCAFailureKeepsUncoveredHTTP(t *testing.T) {
	testDomainExpansionWorker(t, true)
}

func testDomainExpansionWorker(t *testing.T, fail bool) {
	f := newACMEWorkerFixture(t)
	if j := f.run(t, "ssl.issue"); j.State != "done" {
		t.Fatal(j)
	}
	ctx := context.Background()
	f.a.Cfg.ACMEDirectoryProd = f.ca.server.URL + "/directory"
	f.a.Cfg.ACMEEmail = "owner@example.test"
	// Treat this private-CA fixture as production provenance to exercise force HTTPS.
	if err := f.a.Store.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "UPDATE domains SET cert_staging=0 WHERE site_id=?", f.sid); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "UPDATE sites SET force_https=1 WHERE id=?", f.sid)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	// Observe the durable child before the parent returns, without a scheduler tick.
	observed := make(chan []store.Job, 1)
	f.a.Jobs.SetRunner(func(ctx context.Context, j *jobs.Job) error {
		err := f.server.ExecuteJob(ctx, j)
		if j.Verb == "domain.add" {
			children, e := f.a.Store.ListJobs(ctx, store.JobFilter{Verb: "ssl.issue", State: "queued"})
			if e != nil {
				return e
			}
			observed <- children
		}
		return err
	})
	f.ca.mu.Lock()
	f.ca.fail = fail
	f.ca.mu.Unlock()
	raw, _ := json.Marshal(map[string]string{"site_id": f.sid, "fqdn": "alias.example.test"})
	reply := f.server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "domain.add", Args: raw})
	if !reply.OK {
		t.Fatal(reply)
	}
	select {
	case children := <-observed:
		if len(children) != 1 {
			t.Fatalf("domain.add must durably enqueue exactly one immediate expansion, got %+v", children)
		}
		child := children[0]
		if !child.Effectful || child.CreatedBy != "local-root" || child.Role != "owner" || child.ObjectKey != "site:"+f.sid {
			t.Fatalf("child authority/class: %+v", child)
		}
		var args struct {
			SiteID  string `json:"site_id"`
			Staging bool   `json:"staging"`
		}
		if json.Unmarshal(child.Args, &args) != nil || args.SiteID != f.sid || args.Staging {
			t.Fatalf("lost staging provenance: %s", child.Args)
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			j, err := f.a.Store.GetJob(ctx, child.ID)
			if err != nil {
				t.Fatal(err)
			}
			if j.State == "failed" {
				if !fail {
					t.Fatalf("expansion failed: %+v", j)
				}
				ds, err := f.a.Store.ListDomains(ctx, f.sid)
				if err != nil {
					t.Fatal(err)
				}
				for _, d := range ds {
					if d.FQDN == "alias.example.test" && d.CertState != "none" {
						t.Fatal("failed CA claimed coverage", d)
					}
				}
				if _, err := certs.Load(f.a.Cfg.TLSDir, "example.test", []string{"example.test"}, time.Now()); err != nil {
					t.Fatal(err)
				}
				return
			}
			if j.State == "done" {
				if fail {
					t.Fatal("CA failure ignored")
				}
				if _, err := certs.Load(f.a.Cfg.TLSDir, "example.test", []string{"example.test", "alias.example.test"}, time.Now()); err != nil {
					t.Fatal(err)
				}
				ds, err := f.a.Store.ListDomains(ctx, f.sid)
				if err != nil {
					t.Fatal(err)
				}
				for _, d := range ds {
					if d.CertState != "issued" || d.CertStaging {
						t.Fatalf("false or missing coverage: %+v", d)
					}
				}
				f.ca.mu.Lock()
				defer f.ca.mu.Unlock()
				if f.ca.orders != 2 || len(f.ca.domains) != 2 {
					t.Fatalf("orders=%d SANs=%v", f.ca.orders, f.ca.domains)
				}
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("expansion worker timeout")
	case <-time.After(10 * time.Second):
		t.Fatal("domain.add worker timeout")
	}
}
