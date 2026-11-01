package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	php "novapanel/internal/render/php"
	"novapanel/internal/store"
)

type phpGuardClock struct{ now atomic.Int64 }

func (c *phpGuardClock) Now() int64 { return c.now.Load() }

func TestPHPWorkerEffectGuardUsesLiveJournalExpiry(t *testing.T) {
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "local-root", Role: "owner", Effectful: true})
	clock := &phpGuardClock{}
	clock.now.Store(time.Now().UnixMilli())
	s, err := store.OpenWithClock(filepath.Join(t.TempDir(), "panel.db"), clock)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sid, did := "01K8WMXZF57CGNZKX1FZ38DZMA", "01K8WMXZF57CGNZKX1FZ38DZMB"
	version := "8.3"
	if _, err = s.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "php", Docroot: "/srv/sites/demo/public", PHPVersion: &version}, store.Domain{ID: did, FQDN: "demo.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	q := NewQueue(s, nil, &fakeAudit{})
	q.SetClock(clock)
	if err = q.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	req := php.SetRequest{SiteID: sid, Version: version}
	en, err := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.PHPSet, SiteID: sid, PHP: &req}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	finished := make(chan struct{})
	q.SetRunner(func(w context.Context, j *Job) error {
		defer close(finished)
		if err := q.CheckPHPEffect(w, j.ID, sid); err != nil {
			t.Errorf("live check: %v", err)
		}
		if err := q.CheckPHPEffect(w, j.ID, "wrong"); err == nil {
			t.Error("wrong site accepted")
		}
		op, err := s.LoadOperation(w, j.ID)
		if err != nil {
			t.Error(err)
			return err
		}
		clock.now.Store(op.Expires)
		if err := q.CheckPHPEffect(w, j.ID, sid); !errors.Is(err, store.ErrOperationExpired) {
			t.Errorf("expiry: %v", err)
		} else {
			calls++
		}
		return errors.New("stop")
	})
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- q.Run(runCtx) }()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("worker did not run")
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("real expiry not observed")
	}
	if err = q.CheckPHPEffect(ctx, en.Job.ID, sid); err == nil {
		t.Fatal("accepted outside worker")
	}
}
