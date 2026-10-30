package verbs_test

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
)

func TestACMEAdmissionAndFailClosedExecution(t *testing.T) {
	ctx := context.Background()
	st, e := store.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	log, e := audit.Open(st, filepath.Join(t.TempDir(), "audit.log"))
	if e != nil {
		t.Fatal(e)
	}
	defer log.Close()
	sid, _ := id.New()
	did, _ := id.New()
	_, e = st.CreateSite(ctx, store.Site{ID: sid, Name: "acme-demo", Type: "static", Docroot: "/srv/sites/acme-demo/public"}, store.Domain{ID: did, FQDN: "example.test"})
	if e != nil {
		t.Fatal(e)
	}
	if e = st.SetSiteStatus(ctx, sid, "active"); e != nil {
		t.Fatal(e)
	}
	a := agent.New()
	a.Store = st
	a.Audit = log
	server := rpc.NewServer(a)
	server.ConfigureJobs()
	for _, name := range []string{"ssl.issue", "ssl.renew"} {
		verb, ok := rpc.Registry[name]
		if !ok {
			t.Fatalf("missing %s", name)
		}
		if !verb.Effectful || !verb.Slow || verb.MFA || verb.Scope != "sites:write" || verb.MinRole != rpc.Admin {
			t.Fatalf("wrong policy %+v", verb)
		}
		args := json.RawMessage(`{"site_id":"` + sid + `","staging":true}`)
		if name == "ssl.renew" {
			args = json.RawMessage(`{"site_id":"` + sid + `"}`)
		}
		denied := server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Role: "read_only"}), rpc.Request{Verb: name, Args: args})
		if denied.Code != "forbidden" {
			t.Fatal(denied)
		}
		prod := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: name, Args: json.RawMessage(`{"site_id":"` + sid + `","staging":false}`)})
		wantProd := "precondition_failed"
		if name == "ssl.renew" {
			wantProd = "bad_request"
		}
		if prod.Code != wantProd {
			t.Fatal(prod)
		}
		r := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: name, Args: args})
		if name == "ssl.renew" {
			if r.Code != "precondition_failed" {
				t.Fatal(r)
			}
			continue
		}
		if !r.OK {
			t.Fatal(r)
		}
		j := jobs.Job(r.Data.(jobs.EnqueueResult).Job)
		if !j.Effectful || j.ObjectKey != "site:"+sid {
			t.Fatal(j)
		}
		if e = verb.Exec(ctx, a, &j); e == nil {
			t.Fatal("unsafe publication enabled")
		}
	}
}
