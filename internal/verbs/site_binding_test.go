package verbs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/id"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"testing"
)

func TestSiteGetWordPressDatabaseBinding(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sid, _ := id.New()
	domain, _ := id.New()
	db, _ := id.New()
	user, _ := id.New()
	_, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "wpdemo", Type: "wordpress", Docroot: "/srv/sites/wpdemo/public"}, store.Domain{ID: domain, FQDN: "wpdemo.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.CreateDatabasePrimary(ctx, store.DatabasePrimary{ID: db, UserID: user, SiteID: sid}); err != nil {
		t.Fatal(err)
	}
	a := agent.New()
	a.Store = st
	srv := rpc.NewServer(a)
	read := func() map[string]any {
		t.Helper()
		r := srv.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Role: "read_only"}), rpc.Request{Verb: "site.get", Args: json.RawMessage(`{"site_id":"` + sid + `"}`)})
		if !r.OK {
			t.Fatalf("site.get: %+v", r)
		}
		return sessionData(t, r)
	}
	if _, ok := read()["wordpress_database_id"]; ok {
		t.Fatal("unbound site must omit database id")
	}
	if err = st.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "INSERT INTO wordpress_bindings(site_id,db_id) VALUES(?,?)", sid, db)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	got := read()
	if got["wordpress_database_id"] != db {
		t.Fatalf("binding: %+v", got)
	}
}
