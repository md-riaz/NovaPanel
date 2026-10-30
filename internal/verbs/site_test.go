package verbs_test

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/id"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"testing"
)

func TestSiteReadProjection(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := agent.New()
	a.Store = st
	s := rpc.NewServer(a)
	call := func(verb, args string) rpc.Reply {
		return s.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Role: "read_only"}), rpc.Request{Verb: verb, Args: json.RawMessage(args)})
	}
	raw := func(r rpc.Reply) string {
		t.Helper()
		if !r.OK {
			t.Fatalf("%+v", r)
		}
		b, e := json.Marshal(r.Data)
		if e != nil {
			t.Fatal(e)
		}
		return string(b)
	}
	if got := raw(call("site.list", `{}`)); got != `{"sites":[]}` {
		t.Fatal(got)
	}
	sid, _ := id.New()
	did, _ := id.New()
	_, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, store.Domain{ID: did, FQDN: "demo.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if got := raw(call("site.list", `{}`)); got != `{"sites":[{"id":"`+sid+`","name":"demo","type":"static","primary_domain":"demo.example.test","status":"creating","php_version":null}]}` {
		t.Fatal(got)
	}
	got := sessionData(t, call("site.get", `{"site_id":"`+sid+`"}`))
	if got["site"].(map[string]any)["status"] != "creating" || got["cert"].(map[string]any)["state"] != "none" || len(got["domains"].([]any)) != 1 {
		t.Fatal(got)
	}
	missing, _ := id.New()
	if r := call("site.get", `{"site_id":"`+missing+`"}`); r.Code != "not_found" {
		t.Fatal(r)
	}
	if r := call("site.get", `{"site_id":"../bad"}`); r.Code != "invalid_argument" {
		t.Fatal(r)
	}
	for _, verb := range []string{"site.list", "site.get"} {
		v := rpc.Registry[verb]
		if !v.ReadOnly || v.Slow || v.Scope != "sites:read" || v.MinRole != rpc.ReadOnly {
			t.Fatal(v)
		}
	}
	r := s.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Kind: "token", Session: "limited", Scopes: []string{"system:read"}}), rpc.Request{Verb: "site.list", Args: json.RawMessage(`{}`)})
	if r.Code != "forbidden" {
		t.Fatal(r)
	}
}
