package jobs

import (
	"context"
	"database/sql"
	"errors"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
)

func TestSiteDeleteChangedIdentityRefusesRunner(t *testing.T) {
	for _, change := range []string{
		"UPDATE sites SET status='deleting' WHERE id='site'",
		"UPDATE sites SET docroot='/scratch/replaced' WHERE id='site'",
		"UPDATE databases SET site_id=NULL",
		"INSERT INTO db_users(id,db_id,username,host,created) SELECT '01K8WMXZF57CGNZKX1FZ38DZMS',id,'u_01k8wmxzf57cgnzkx1fz38dzms','127.0.0.1',1 FROM databases",
		"INSERT INTO wordpress_bindings(site_id,db_id) SELECT site_id,id FROM databases",
	} {
		t.Run(change, func(t *testing.T) {
			ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "session-owner", Role: "owner"})
			s, e := store.Open(filepath.Join(t.TempDir(), "panel.db"))
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			_, e = s.CreateSite(ctx, store.Site{ID: "site", Name: "example", Type: "php", Docroot: "/scratch/example"}, store.Domain{ID: "domain", FQDN: "example.test"})
			if e != nil {
				t.Fatal(e)
			}
			if e = s.SetSiteStatus(ctx, "site", "active"); e != nil {
				t.Fatal(e)
			}
			pair := store.DatabasePrimary{ID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR", SiteID: "site"}
			if e = s.CreateDatabasePrimary(ctx, pair); e != nil {
				t.Fatal(e)
			}
			q := NewQueue(s, nil, &fakeAudit{})
			if e = q.PrepareOperations(ctx); e != nil {
				t.Fatal(e)
			}
			r, e := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.SiteDelete, SiteID: "site", PurgeDatabases: true}, nil)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Tx(ctx, func(tx *sql.Tx) error { _, e := tx.ExecContext(ctx, change); return e }); e != nil {
				t.Fatal(e)
			}
			calls := 0
			q.SetRunner(func(context.Context, *Job) error { calls++; return nil })
			if e = q.Run(ctx); !errors.Is(e, ErrRecoveryBlocked) {
				t.Fatalf("startup: %v", e)
			}
			j, e := s.GetJob(ctx, r.Job.ID)
			if e != nil || calls != 0 || j.Code != "conflict" {
				t.Fatalf("calls=%d job=%+v %v", calls, j, e)
			}
			if _, e = s.SiteByID(ctx, "site"); e != nil {
				t.Fatal("site deleted", e)
			}
		})
	}
}
