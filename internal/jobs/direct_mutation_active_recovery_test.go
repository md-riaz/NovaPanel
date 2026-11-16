package jobs

import (
	"context"
	"database/sql"
	"errors"
	"novapanel/internal/store"
	"sync"
	"testing"
	"time"
)

type admissionObservedContext struct {
	context.Context
	reached chan struct{}
	once    sync.Once
}

func (c *admissionObservedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.reached) })
	return c.Context.Done()
}

func TestDirectMutationsWaitForActiveStartupRecovery(t *testing.T) {
	q, s, _, ctx := fixture(t)
	_, err := s.CreateSite(ctx, store.Site{ID: "site", Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, store.Domain{ID: "primary", FQDN: "example.test"})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := q.Enqueue(ctx, "domain.add", nil, "site:site")
	if err != nil {
		t.Fatal(err)
	}
	claim, err := q.Enqueue(ctx, "reconcile", nil, "system")
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	finishRecovery := func() { releaseOnce.Do(func() { close(release) }) }
	q.SetStartupRecovery(func(context.Context) (*Job, error) { close(entered); <-release; return nil, nil }, nil)
	startup := make(chan error, 1)
	startupDone := make(chan struct{})
	go func() { defer close(startupDone); startup <- q.PrepareStartup(ctx) }()
	t.Cleanup(func() {
		finishRecovery()
		select {
		case <-startupDone:
		case <-time.After(3 * time.Second):
			t.Error("startup did not exit during cleanup")
		}
	})
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("startup did not enter recovery")
	}
	type result struct {
		name string
		err  error
	}
	results := make(chan result, 2)
	wait, cancel := context.WithCancel(ctx)
	defer cancel()
	dctx := &admissionObservedContext{Context: wait, reached: make(chan struct{})}
	cctx := &admissionObservedContext{Context: wait, reached: make(chan struct{})}
	go func() {
		_, e := q.AddDomain(dctx, store.Domain{ID: "alias", SiteID: "site", FQDN: "alias.example.test"}, parent.Job.ID)
		results <- result{"domain", e}
	}()
	go func() {
		e := q.PublishClaim(cctx, claim.Job.ID, map[string]any{"password": "secret"})
		results <- result{"claim", e}
	}()
	for _, ch := range []<-chan struct{}{dctx.reached, cctx.reached} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("mutation never attempted guard")
		}
	}
	select {
	case r := <-results:
		t.Fatalf("%s did not wait for recovery: %v", r.name, r.err)
	default:
	}
	cancel()
	for range 2 {
		select {
		case r := <-results:
			if !errors.Is(r.err, context.Canceled) {
				t.Errorf("%s: %v", r.name, r.err)
			}
		case <-time.After(time.Second):
			t.Fatal("mutation failed to cancel")
		}
	}
	var domains, children, claims, cipher int
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM domains WHERE id='alias'").Scan(&domains); e != nil {
			return e
		}
		if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE verb IN ('ssl.issue','ssl.renew') AND object_key='site:site'").Scan(&children); e != nil {
			return e
		}
		return tx.QueryRowContext(ctx, "SELECT count(*), coalesce(sum(length(ciphertext)),0) FROM job_claims WHERE job_id=?", claim.Job.ID).Scan(&claims, &cipher)
	})
	if err != nil {
		t.Fatal(err)
	}
	if domains != 0 || children != 0 || claims != 1 || cipher != 0 {
		t.Fatalf("unexpected effects domain=%d child=%d claims=%d ciphertext=%d", domains, children, claims, cipher)
	}
	finishRecovery()
	select {
	case err = <-startup:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("startup did not finish")
	}
	if _, err = q.AddDomain(ctx, store.Domain{ID: "alias", SiteID: "site", FQDN: "alias.example.test"}, parent.Job.ID); err != nil {
		t.Fatalf("valid domain after recovery: %v", err)
	}
	if err = q.PublishClaim(ctx, claim.Job.ID, map[string]any{"password": "secret"}); err != nil {
		t.Fatalf("claim after recovery: %v", err)
	}
}
