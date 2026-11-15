package verbs_test

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/id"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
)

func TestSiteDeleteAttachedDatabaseThroughQueue(t *testing.T) {
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
	a.Store, a.Audit = st, log
	a.Cfg.SitesRoot = filepath.Join(root, "sites")
	sid, _ := id.New()
	domain, _ := id.New()
	dbid, _ := id.New()
	uid, _ := id.New()
	_, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: filepath.Join(a.Cfg.SitesRoot, "demo", "public")}, store.Domain{ID: domain, FQDN: "demo.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.ReserveDatabase(ctx, store.Database{ID: dbid, SiteID: &sid, Name: "attached_db", Engine: "mariadb"}, store.DatabaseUser{ID: uid, DatabaseID: dbid, Username: "attached_user"}); err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer(a)
	server.ConfigureJobs()
	if err := a.Jobs.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	call := func(verb, args string) rpc.Reply {
		return server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: verb, Args: json.RawMessage(args)})
	}
	if r := call("site.delete", `{"site_id":"../bad"}`); r.Code != "invalid_argument" {
		t.Fatalf("invalid input: %+v", r)
	}
	r := call("site.delete", `{"site_id":"`+sid+`"}`)
	if r.Code != "precondition_failed" {
		t.Fatalf("attached database must prevent deletion: %+v", r)
	}
	if _, err = st.SiteByID(ctx, sid); err != nil {
		t.Fatal(err)
	}
}
