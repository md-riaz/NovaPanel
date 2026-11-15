package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestDestructiveTeardownSurvivesExpiryAndReplays(t *testing.T) {
	ctx := context.Background()
	clock := &authClock{now: 1000}
	s, err := OpenWithClock(filepath.Join(t.TempDir(), "state.db"), clock)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	makeSite := func(site, domain, name string) {
		_, e := s.CreateSite(ctx, Site{ID: site, Name: name, Type: "static", Docroot: "/srv/sites/" + name + "/public"}, Domain{ID: domain, FQDN: name + ".test"})
		if e != nil {
			t.Fatal(e)
		}
		if e = s.SetSiteStatus(ctx, site, "active"); e != nil {
			t.Fatal(e)
		}
	}
	makeSite("site1", "domain1", "first")
	makeSite("site2", "domain2", "second")
	pair := DatabasePrimary{ID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR", SiteID: "site1"}
	if err = s.CreateDatabasePrimary(ctx, pair); err != nil {
		t.Fatal(err)
	}
	makeJob := func(id, site string) Operation {
		j := Job{ID: id, Verb: "site.delete", CreatedBy: "owner", State: "queued", Args: []byte(`{}`)}
		op, e := s.PrepareOperation(ctx, j, JobClaim{}, OperationRequest{Kind: SiteDelete, SiteID: site, PurgeDatabases: true}, nil, nil)
		if e != nil {
			t.Fatal(e)
		}
		return op
	}
	begun := makeJob("begun", "site1")
	makeJob("unstarted", "site2")
	if _, err = s.StartJob(ctx, begun.JobID, clock.now); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckpointOperation(ctx, begun.JobID, "prepared", "teardown_started"); err != nil {
		t.Fatal(err)
	}
	clock.now = begun.Expires
	if n, e := s.ExpireOperations(ctx); e != nil || n != 1 {
		t.Fatalf("expire: %d %v", n, e)
	}
	old, err := s.LoadOperation(ctx, "unstarted")
	if err != nil || old.Checkpoint != "expired" {
		t.Fatalf("unstarted: %+v %v", old, err)
	}
	if err = s.RecoverJobs(ctx, clock.now); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingOperations(ctx)
	if err != nil || len(pending) != 1 || pending[0].ID != "begun" {
		t.Fatalf("pending: %+v %v", pending, err)
	}
	if err = s.CheckOperation(ctx, "begun"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartJob(ctx, "begun", clock.now); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckpointOperation(ctx, "begun", "teardown_started", "db_applied"); err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteOperation(ctx, "begun", nil); err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteOperation(ctx, "begun", nil); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if _, err = s.GetDatabase(ctx, pair.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("database remains: %v", err)
	}
	if _, err = s.DatabaseUserByID(ctx, pair.UserID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("user remains: %v", err)
	}
	if _, err = s.SiteByID(ctx, "site1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("site remains: %v", err)
	}
}
