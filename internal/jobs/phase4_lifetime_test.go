package jobs

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	runner "novapanel/internal/exec"
	"novapanel/internal/rpcerr"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
	"novapanel/internal/wordpress/configcustody"
)

type fakeInstallLifetime struct {
	held, empty, closed bool
	closeErr            error
	runs                int
}

type lifetimeAdmissionStore struct {
	*store.Store
	op                  store.Operation
	status              string
	freshErr, replayErr error
}

func (s *lifetimeAdmissionStore) LoadOperation(context.Context, string) (store.Operation, error) {
	return s.op, nil
}
func (s *lifetimeAdmissionStore) SiteByID(context.Context, string) (store.Site, error) {
	return store.Site{Status: s.status}, nil
}
func (s *lifetimeAdmissionStore) CheckFreshSiteLifetime(context.Context, store.SiteLifetime) error {
	return s.freshErr
}
func (s *lifetimeAdmissionStore) CheckActivatedSiteLifetime(context.Context, store.SiteLifetime, string) error {
	return s.replayErr
}

func TestWithFreshWordPressLifetimeAdmission(t *testing.T) {
	jid, site := "01ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	identity := store.SiteLifetime{SiteID: site, Name: "example", Docroot: "/sites/example/public", AllocatingJob: jid, UID: 123, GID: 456}
	fake := &lifetimeAdmissionStore{status: "creating", freshErr: store.ErrSitePrecondition, replayErr: store.ErrSitePrecondition}
	fake.op = store.Operation{Kind: store.WordPressInstall, SiteID: site, Identity: store.OperationIdentity{WordPress: &store.WordPressReservation{Input: wordpress.InstallInput{SitesRoot: "/sites", SiteName: "example", UID: 123, GID: 456}}}}
	q := NewQueue(fake, nil, &fakeAudit{})
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	lease := &configWorkerLease{queue: q, job: jid, ctx: base, cancel: cancel}
	lease.active.Store(true)
	worker := context.WithValue(base, configWorkerKey{}, lease)
	held := map[string]bool{}
	for _, key := range fake.op.Keys() {
		held[key] = true
	}
	locked := context.WithValue(worker, heldLocksKey{}, held)
	call := func(ctx context.Context, id store.SiteLifetime) error {
		return q.WithFreshWordPressLifetime(ctx, jid, id, nil, nil)
	}
	if err := call(base, identity); err == nil {
		t.Fatal("missing worker accepted")
	}
	lease.active.Store(false)
	if err := call(locked, identity); err == nil {
		t.Fatal("revoked worker accepted")
	}
	lease.active.Store(true)
	if err := call(worker, identity); err == nil {
		t.Fatal("missing operation locks accepted")
	}
	lease.ctx = context.WithValue(base, heldLocksKey{}, held)
	changed := identity
	changed.UID++
	if err := call(locked, changed); err == nil {
		t.Fatal("identity mismatch accepted")
	}
	fake.status = "active"
	var coded *rpcerr.CodedError
	if err := call(locked, identity); !errors.As(err, &coded) || coded.Code != "precondition_failed" {
		t.Fatalf("status: %v", err)
	}
	fake.status = "creating"
	coded = nil
	if err := call(locked, identity); !errors.As(err, &coded) || coded.Code != "precondition_failed" {
		t.Fatalf("served: %v", err)
	}
}

func (d *fakeInstallLifetime) WithEmpty(ctx context.Context, fn func(context.Context) error) error {
	if !d.held || !d.empty || d.closed {
		return errors.New("domain not held empty")
	}
	return fn(ctx)
}
func (d *fakeInstallLifetime) Run(ctx context.Context, _ string, args []string, o runner.RunOpts) ([]byte, []byte, int, error) {
	if !d.held || !d.empty || d.closed || o.Uid != 123 || o.Gid != 456 || len(o.Groups) != 0 {
		return nil, nil, -1, errors.New("unsafe admission")
	}
	d.runs++
	for _, arg := range args {
		if arg == "is-installed" {
			return nil, nil, 1, nil
		}
	}
	return nil, nil, 0, nil
}
func (d *fakeInstallLifetime) Close(context.Context) error { d.closed = true; return d.closeErr }

func fakeLifetimePlan() wordpress.Plan {
	return wordpress.Plan{Steps: []wordpress.Step{{Kind: wordpress.Download, Program: "/stub"}, {Kind: wordpress.VerifyChecksums, Program: "/stub"}, {Kind: wordpress.IsInstalled, Program: "/stub", Args: []string{"is-installed"}}, {Kind: wordpress.InstallCore, Program: "/stub"}}}
}
func TestHeldLifetimeIsolationAndRelease(t *testing.T) {
	for _, mode := range []string{"success", "callback", "close", "early", "duplicate", "panic"} {
		t.Run(mode, func(t *testing.T) {
			domain := &fakeInstallLifetime{held: true, empty: true}
			if mode == "close" {
				domain.closeErr = errors.New("cannot confirm empty")
			}
			binding := configcustody.ConfigBinding{JobID: "job", SiteID: "site", UID: 123, GID: 456}
			identity := store.SiteLifetime{SiteID: "site", UID: 123, GID: 456}
			activated, effects, releases := 0, 0, 0
			l := &WordPressLifetime{domain: domain, binding: binding, identity: identity, plan: fakeLifetimePlan(), check: func(context.Context) error { return nil }, activate: func(_ context.Context, got store.SiteLifetime) error {
				if !domain.closed || got != identity {
					t.Fatal("activation before drain or with changed identity")
				}
				activated++
				return nil
			}}
			err := withInstallLifetime(context.Background(), l, func(l *WordPressLifetime) error {
				if mode == "early" {
					return l.activateServing(context.Background())
				}
				if mode == "panic" {
					panic("fixture")
				}
				for _, kind := range []wordpress.StepKind{wordpress.Download, wordpress.VerifyChecksums} {
					if _, _, _, err := l.runReviewed(context.Background(), reviewedInstallChild{kind: kind}); err != nil {
						return err
					}
				}
				err := l.ConfigIsolation(context.Background(), binding, func(context.Context) error {
					if !domain.held || !domain.empty || domain.closed {
						t.Fatal("effect outside held empty domain")
					}
					effects++
					if mode == "callback" {
						return errors.New("effect failed")
					}
					return nil
				})
				if err != nil {
					return err
				}
				if mode == "duplicate" {
					_ = l.ConfigIsolation(context.Background(), binding, func(context.Context) error { effects++; return nil })
				}
				if _, _, _, err = l.runReviewed(context.Background(), reviewedInstallChild{kind: wordpress.IsInstalled}); err != nil {
					return err
				}
				_, _, _, err = l.runReviewed(context.Background(), reviewedInstallChild{kind: wordpress.InstallCore})
				return err
			}, func(l *WordPressLifetime) error {
				releases++
				if activated != 1 {
					t.Error("serving before activation")
				}
				return nil
			})
			if !domain.closed {
				t.Fatal("domain not closed")
			}
			if mode == "success" {
				if err != nil || effects != 1 || releases != 1 || activated != 1 || domain.runs != 4 {
					t.Fatalf("success: %v effects=%d release=%d activation=%d runs=%d", err, effects, releases, activated, domain.runs)
				}
			} else if err == nil || releases != 0 || activated != 0 {
				t.Fatalf("failure released: %v release=%d activation=%d", err, releases, activated)
			}
			if effects > 1 {
				t.Fatal("callback executed twice")
			}
			if err := l.activateServing(context.Background()); err == nil {
				t.Fatal("release after lifetime")
			}
		})
	}
}

// Returning with a retained child must cancel and drain it before releasing exclusion.
type blockingInstallLifetime struct {
	started chan struct{}
	done    chan struct{}
	closed  bool
}

func (d *blockingInstallLifetime) WithEmpty(context.Context, func(context.Context) error) error {
	return nil
}
func (d *blockingInstallLifetime) Run(ctx context.Context, _ string, _ []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	close(d.started)
	<-ctx.Done()
	close(d.done)
	return nil, nil, -1, ctx.Err()
}
func (d *blockingInstallLifetime) Close(context.Context) error {
	select {
	case <-d.done:
	default:
		return errors.New("child survived teardown")
	}
	d.closed = true
	return nil
}
func TestLifetimeReturnCancelsRetainedChild(t *testing.T) {
	d := &blockingInstallLifetime{started: make(chan struct{}), done: make(chan struct{})}
	activated := false
	l := &WordPressLifetime{domain: d, identity: store.SiteLifetime{UID: 123, GID: 456}, plan: fakeLifetimePlan(), check: func(context.Context) error { return nil }, activate: func(context.Context, store.SiteLifetime) error { activated = true; return nil }}
	err := withInstallLifetime(context.Background(), l, func(l *WordPressLifetime) error {
		go func() {
			_, _, _, _ = l.runReviewed(context.Background(), reviewedInstallChild{kind: wordpress.Download})
		}()
		<-d.started
		return nil
	}, func(l *WordPressLifetime) error { return nil })
	if err == nil || !d.closed || activated {
		t.Fatalf("retained child: %v closed=%v activated=%v", err, d.closed, activated)
	}
}

func TestLifetimeReplayKeepsSiteIdentity(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	jobID, siteID := "01ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	identity := store.SiteLifetime{SiteID: siteID, Name: "example", Docroot: "/sites/example/public", AllocatingJob: jobID, UID: 123, GID: 456}
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`INSERT INTO jobs(id,verb,args_json,object_key,actor,created_by,state,created,updated) VALUES('` + jobID + `','wp.install','{}','site:example','fixture','owner','running',1,1)`,
			`INSERT INTO sites(id,name,type,docroot,status,created,updated,lifetime_state) VALUES('` + siteID + `','example','wordpress','/sites/example/public','creating',1,1,'provisioning')`,
			`INSERT INTO site_account_provenance(site_id,account_name,uid,gid,allocating_job,evidence) VALUES('` + siteID + `','example',123,456,'` + jobID + `','owned')`,
			`INSERT INTO phase4_operations(job_id,kind,site_id,checkpoint,identities_json,expires,updated) VALUES('` + jobID + `','wordpress_install','` + siteID + `','verified','{}',9999999999999,1)`,
		} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	binding := configcustody.ConfigBinding{JobID: jobID, SiteID: siteID, UID: 123, GID: 456}
	d := &fakeInstallLifetime{held: true, empty: true}
	l := &WordPressLifetime{domain: d, binding: binding, identity: identity, plan: fakeLifetimePlan(), check: func(context.Context) error { return nil }, activate: func(ctx context.Context, got store.SiteLifetime) error {
		if err := s.ActivateSiteLifetime(ctx, got); err != nil {
			return err
		}
		return errors.New("injected interruption after commit")
	}}
	err = withInstallLifetime(ctx, l, func(l *WordPressLifetime) error {
		for _, kind := range []wordpress.StepKind{wordpress.Download, wordpress.VerifyChecksums} {
			if _, _, _, err := l.runReviewed(ctx, reviewedInstallChild{kind: kind}); err != nil {
				return err
			}
		}
		if err := l.ConfigIsolation(ctx, binding, func(context.Context) error { return nil }); err != nil {
			return err
		}
		if _, _, _, err := l.runReviewed(ctx, reviewedInstallChild{kind: wordpress.IsInstalled}); err != nil {
			return err
		}
		_, _, _, err := l.runReviewed(ctx, reviewedInstallChild{kind: wordpress.InstallCore})
		return err
	}, func(*WordPressLifetime) error { t.Error("enabled serving after interrupted activation"); return nil })
	if err == nil {
		t.Fatal("interruption ignored")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reloaded, err := s.LoadSiteLifetime(ctx, siteID)
	if err != nil || reloaded != identity {
		t.Fatalf("identity reload: %+v %v", reloaded, err)
	}
	changed := reloaded
	changed.UID++
	if err := s.CheckActivatedSiteLifetime(ctx, changed, jobID); !errors.Is(err, store.ErrSitePrecondition) {
		t.Fatalf("changed replay identity: %v", err)
	}
	if err := s.CheckActivatedSiteLifetime(ctx, reloaded, "another-job"); !errors.Is(err, store.ErrSitePrecondition) {
		t.Fatalf("changed replay job: %v", err)
	}
	d = &fakeInstallLifetime{held: true, empty: true}
	released := false
	err = replayActivatedLifetime(ctx, d, reloaded, func(ctx context.Context) error { return s.CheckActivatedSiteLifetime(ctx, reloaded, jobID) }, func(l *WordPressLifetime) error {
		if !d.closed || l.identity != reloaded {
			t.Error("incorrect replay release")
		}
		released = true
		return nil
	})
	if err != nil || !released {
		t.Fatalf("replay: released=%v err=%v", released, err)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE jobs SET state='done' WHERE id=?`, jobID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckActivatedSiteLifetime(ctx, reloaded, jobID); !errors.Is(err, store.ErrSitePrecondition) {
		t.Fatalf("terminal replay: %v", err)
	}
}
