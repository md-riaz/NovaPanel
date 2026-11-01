package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	certs "novapanel/internal/tls"
)

func TestIndependentD1QueueDedupReplay(t *testing.T) {
	testDomainExpansionSharedReplay(t, true)
}

func TestDomainExpansionSharedFullSAN(t *testing.T) {
	testDomainExpansionSharedReplay(t, false)
}

func testDomainExpansionSharedReplay(t *testing.T, fail bool) {
	f := newACMEWorkerFixture(t)
	original := f.run(t, "ssl.issue")
	if original.State != "done" {
		t.Fatal(original)
	}
	ctx := context.Background()
	f.a.Cfg.ACMEDirectoryProd = f.ca.server.URL + "/directory"
	f.a.Cfg.ACMEEmail = "owner@example.test"
	if e := f.a.Store.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "UPDATE domains SET cert_staging=0")
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE sites SET force_https=1")
		return e
	}); e != nil {
		t.Fatal(e)
	}
	f.ca.mu.Lock()
	f.ca.fail = fail
	f.ca.mu.Unlock()
	entered := make(chan struct{})
	release := make(chan struct{})
	first := true
	f.a.Jobs.SetRunner(func(ctx context.Context, j *jobs.Job) error {
		if j.Verb == "domain.add" && first {
			first = false
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		e := f.server.ExecuteJob(ctx, j)
		if j.Verb == "domain.add" {
			var a struct {
				FQDN string `json:"fqdn"`
			}
			json.Unmarshal(j.Args, &a)
			if a.FQDN == "b.example.test" && e == nil {
				return errors.New("injected post-render failure")
			}
		}
		return e
	})
	dispatch := func(fqdn string) jobs.EnqueueResult {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"site_id": f.sid, "fqdn": fqdn})
		r := f.server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "domain.add", Args: raw})
		if !r.OK {
			t.Fatal(r)
		}
		return r.Data.(jobs.EnqueueResult)
	}
	wait := func(id string) store.Job {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			j, e := f.a.Store.GetJob(ctx, id)
			if e != nil {
				t.Fatal(e)
			}
			if j.State == "done" || j.State == "failed" {
				return j
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("timeout")
		return store.Job{}
	}
	a := dispatch("a.example.test")
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("first parent did not enter worker")
	}
	b := dispatch("b.example.test")
	close(release)
	wait(a.Job.ID)
	wait(b.Job.ID)
	children, e := f.a.Store.ListJobs(ctx, store.JobFilter{Verb: "ssl.issue"})
	if e != nil {
		t.Fatal(e)
	}
	if len(children) != 2 {
		t.Fatalf("expected original issue plus one shared expansion: %+v", children)
	}
	for _, c := range children {
		terminal := wait(c.ID)
		if c.CreatedBy != "local-root" || c.Role != "owner" || !c.Effectful || c.ObjectKey != "site:"+f.sid {
			t.Fatalf("child authority: %+v", c)
		}
		if c.ID != original.ID && (terminal.State == "failed") != fail {
			t.Fatalf("shared expansion outcome: %+v", terminal)
		}
	}
	f.ca.mu.Lock()
	orders := f.ca.orders
	f.ca.mu.Unlock()
	before := len(children)
	retry := dispatch("b.example.test")
	wait(retry.Job.ID)
	children, e = f.a.Store.ListJobs(ctx, store.JobFilter{Verb: "ssl.issue"})
	if e != nil {
		t.Fatal(e)
	}
	if len(children) != before {
		t.Fatalf("actual Dispatch/Queue retry after shared child failed created another effectful job: before=%d after=%d", before, len(children))
	}
	f.ca.mu.Lock()
	if f.ca.orders != orders || orders != 2 {
		t.Errorf("CA replay: before=%d after=%d", orders, f.ca.orders)
	}
	f.ca.mu.Unlock()
	covered := []string{"example.test"}
	if !fail {
		covered = append(covered, "a.example.test", "b.example.test")
	}
	if _, e := certs.Load(f.a.Cfg.TLSDir, "example.test", covered, time.Now()); e != nil {
		t.Fatal(e)
	}
}
