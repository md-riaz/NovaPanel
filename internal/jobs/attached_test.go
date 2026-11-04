package jobs

import (
	"context"
	"database/sql"
	"errors"
	"novapanel/internal/rpcerr"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestAttachedDatabaseLockDiscovery(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "stable", true: "changed"}[changed], func(t *testing.T) { testAttachedDatabaseLockDiscovery(t, changed) })
	}
}
func testAttachedDatabaseLockDiscovery(t *testing.T, changed bool) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	sid := "01K8WMXZF57CGNZKX1FZ38DZMA"
	if _, err = s.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, store.Domain{ID: "01K8WMXZF57CGNZKX1FZ38DZMB", FQDN: "demo.test"}); err != nil {
		t.Fatal(err)
	}
	pair := store.DatabasePrimary{ID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR", SiteID: sid}
	if err = s.CreateDatabasePrimary(ctx, pair); err != nil {
		t.Fatal(err)
	}
	second := store.DatabasePrimary{ID: "01K8WMXZF57CGNZKX1FZ38DZMK", UserID: "01K8WMXZF57CGNZKX1FZ38DZMN", SiteID: sid}
	if err = s.CreateDatabasePrimary(ctx, second); err != nil {
		t.Fatal(err)
	}
	q := NewQueue(s, nil, &fakeAudit{})
	if !changed {
		_, e := q.lockJob(ctx, Job{Verb: "site.delete", ObjectKey: "site:" + sid})
		var coded *rpcerr.CodedError
		if !errors.As(e, &coded) || coded.Code != "unsupported" {
			t.Fatalf("unimplemented consumer: %v", e)
		}
	}
	// A held DB lock must not hold SQLite or keep the discovery site lock.
	release := q.locks.lock("db:" + pair.ID)
	done := make(chan error, 1)
	go func() {
		dbs, u, e := q.lockAttachedDatabases(ctx, sid)
		if e == nil {
			defer u()
			if len(dbs) != 2 || dbs[0].ID != second.ID || dbs[1].ID != pair.ID {
				e = errors.New("wrong identities")
			}
		}
		done <- e
	}()
	deadline := time.After(time.Second)
	for {
		q.locks.mu.Lock()
		ref := q.locks.keys["db:"+pair.ID]
		waiting := ref.refs > 1
		q.locks.mu.Unlock()
		if waiting {
			break
		}
		select {
		case <-deadline:
			t.Fatal("did not reach full lock acquisition")
		case <-time.After(time.Millisecond):
		}
	}
	if _, err = s.GetDatabase(ctx, pair.ID); err != nil {
		t.Fatal(err)
	}
	if changed {
		if err = s.Tx(ctx, func(tx *sql.Tx) error {
			_, e := tx.ExecContext(ctx, "UPDATE databases SET site_id=NULL WHERE id=?", pair.ID)
			return e
		}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-done:
		t.Fatal("did not reserve attached DB lock")
	default:
	}
	release()
	select {
	case err = <-done:
		if changed {
			if !errors.Is(err, store.ErrSiteConflict) {
				t.Fatalf("membership change: %v", err)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("deadlock")
	}
}
