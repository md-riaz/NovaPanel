package jobs

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"novapanel/internal/secret"
	"novapanel/internal/store"
)

func TestWaitingSiteDeleteDoesNotBlockUnrelatedAdmission(t *testing.T) {
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "local-root", Role: "owner"})
	s, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	site, err := s.CreateSite(ctx, store.Site{ID: "site", Name: "example", Type: "php", Docroot: "/scratch/example"}, store.Domain{ID: "domain", FQDN: "example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetSiteStatus(ctx, site.ID, "active"); err != nil {
		t.Fatal(err)
	}
	seal, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	q := NewQueue(s, seal, &fakeAudit{})
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	release := q.locks.lock("site:" + site.ID)
	released := false
	defer func() {
		if !released {
			release()
		}
	}()
	deletion := make(chan error, 1)
	go func() {
		_, e := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.SiteDelete, SiteID: site.ID}, nil)
		deletion <- e
	}()
	// Observe the actual waiter, rather than assuming the goroutine has started.
	deadline := time.Now().Add(3 * time.Second)
	for {
		q.locks.mu.Lock()
		refs := q.locks.keys["site:"+site.ID].refs
		q.locks.mu.Unlock()
		if refs == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("site delete did not reach site lock")
		}
		time.Sleep(time.Millisecond)
	}
	admitted := make(chan error, 1)
	go func() {
		_, e := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.DatabaseCreate}, map[string]any{"password": "private"})
		admitted <- e
	}()
	var blocked bool
	select {
	case err = <-admitted:
	case <-time.After(time.Second):
		blocked = true
	}
	release()
	released = true
	if blocked {
		err = <-admitted
	}
	if e := <-deletion; e != nil {
		t.Fatal(e)
	}
	if err != nil {
		t.Fatal(err)
	}
	if blocked {
		t.Fatal("unrelated admission waited for the busy site's lock")
	}
}
