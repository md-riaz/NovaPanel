package jobs

import (
	"context"
	"database/sql"
	"errors"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestSiteDeleteQueueCancelReopen(t *testing.T) {
	for _, purge := range []bool{false, true} {
		t.Run(map[bool]string{false: "retain", true: "purge"}[purge], func(t *testing.T) {
			ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "session-owner", Role: "owner", Actor: "display-only"})
			path := filepath.Join(t.TempDir(), "panel.db")
			s, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { s.Close() }()
			site, err := s.CreateSite(ctx, store.Site{ID: "site", Name: "example", Type: "php", Docroot: "/scratch/example"}, store.Domain{ID: "domain", FQDN: "example.test"})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.SetSiteStatus(ctx, site.ID, "active"); err != nil {
				t.Fatal(err)
			}
			pair := store.DatabasePrimary{ID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR", SiteID: site.ID}
			if err = s.CreateDatabasePrimary(ctx, pair); err != nil {
				t.Fatal(err)
			}
			q := NewQueue(s, nil, &fakeAudit{})
			if err = q.PrepareOperations(ctx); err != nil {
				t.Fatal(err)
			}
			admitted, err := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.SiteDelete, SiteID: site.ID, PurgeDatabases: purge, PurgeFiles: !purge}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if admitted.ClaimToken != "" {
				t.Fatal("delete reserved credential delivery")
			}
			if admitted.Job.ObjectKey != "site:"+site.ID {
				t.Fatal(admitted.Job.ObjectKey)
			}
			run, cancel := context.WithCancel(ctx)
			applied := make(chan struct{})
			effects := 0
			q.SetRunner(func(c context.Context, j *Job) error {
				effects++
				if e := s.CheckpointOperation(c, j.ID, "prepared", "db_applied"); e != nil {
					return e
				}
				close(applied)
				<-c.Done()
				return c.Err()
			})
			done := make(chan error, 1)
			go func() { done <- q.Run(run) }()
			select {
			case <-applied:
			case <-time.After(3 * time.Second):
				t.Fatal("runner not called")
			}
			cancel()
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			q = NewQueue(s, nil, &fakeAudit{})
			q.SetRunner(func(c context.Context, j *Job) error {
				m, _ := MetadataFrom(c)
				if m.CreatedBy != "session-owner" || m.Actor != "display-only" {
					t.Error("lost authority")
				}
				op, e := s.LoadOperation(c, j.ID)
				if e != nil {
					return e
				}
				if op.Checkpoint != "db_applied" {
					effects++
					t.Error("repeated effect")
				}
				if op.Identity.PurgeDatabases != purge || op.Identity.PurgeFiles == purge {
					t.Error("lost independent flags")
				}
				return nil
			})
			run, cancel = context.WithCancel(ctx)
			done = make(chan error, 1)
			go func() { done <- q.Run(run) }()
			deadline := time.After(3 * time.Second)
			for {
				j, e := s.GetJob(ctx, admitted.Job.ID)
				if e == nil && j.State == "done" {
					break
				}
				select {
				case e := <-done:
					t.Fatalf("early exit %v", e)
				case <-deadline:
					t.Fatal("not completed")
				case <-time.After(time.Millisecond):
				}
			}
			cancel()
			if err = <-done; err != nil {
				t.Fatal(err)
			}

			if effects != 1 {
				t.Fatalf("effects %d", effects)
			}
			db, err := s.GetDatabase(ctx, pair.ID)
			if purge {
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("purge: %+v %v", db, err)
				}
			} else if err != nil || db.SiteID != "" {
				t.Fatalf("retain: %+v %v", db, err)
			}
			if _, err = s.SiteByID(ctx, site.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("site remains", err)
			}
			if _, err = s.ConsumeClaim(ctx, admitted.Job.ID, "", "session-owner"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("claim created")
			}
			if err = s.CompleteOperation(ctx, admitted.Job.ID, nil); err != nil {
				t.Fatal("replay", err)
			}
		})
	}
}
