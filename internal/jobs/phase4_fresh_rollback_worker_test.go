package jobs

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"novapanel/internal/rpcerr"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
)

type rollbackIntentFailStore struct {
	*store.Store
	fail        bool
	failPersist bool
}

func (s *rollbackIntentFailStore) PersistFreshWordPressRollbackIntent(ctx context.Context, jid, code string) error {
	if s.failPersist {
		s.failPersist = false
		return errors.New("injected intent write failure")
	}
	return s.Store.PersistFreshWordPressRollbackIntent(ctx, jid, code)
}

func (s *rollbackIntentFailStore) BeginFreshWordPressRollback(ctx context.Context, jid, code string) error {
	if s.fail {
		s.fail = false
		return store.ErrSitePrecondition
	}
	return s.Store.BeginFreshWordPressRollback(ctx, jid, code)
}

func TestFreshWordPressWorkerRollbackAndNameReuse(t *testing.T) {
	q, s, _, ctx := fixture(t)
	if err := q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	m := wordpress.ApprovedArtifacts()
	reservation := store.WordPressReservation{SiteID: "01K8WMXZF57CGNZKX1FZ38DZMA", DomainID: "01K8WMXZF57CGNZKX1FZ38DZMB", Input: wordpress.InstallInput{SitesRoot: "/sites", SiteName: "example", PrimaryDomain: "example.test", Locale: m.Locale, Administrator: wordpress.Administrator{Title: "Example", Username: "Admin", Email: "owner@example.test"}}, Evidence: wordpress.ArtifactEvidence{CLISHA256: m.CLI.SHA256, WordPressSHA256: m.WordPress.SHA256, ChecksumsSHA256: m.ChecksumsSHA256, SignatureVerified: true, SignerFingerprint: m.CLISignerFingerprint}}
	version := "8.3"
	site := store.Site{ID: reservation.SiteID, Name: "example", Type: "wordpress", Docroot: "/sites/example/public", PHPVersion: &version}
	domain := store.Domain{ID: reservation.DomainID, FQDN: "example.test"}
	credentials, err := GenerateWordPressCredentials()
	if err != nil {
		t.Fatal(err)
	}
	en, err := q.EnqueueFreshWordPress(ctx, reservation, site, domain, store.PHPLimits{}, credentials)
	if err != nil {
		t.Fatal(err)
	}
	q.store = &rollbackIntentFailStore{Store: s, fail: true, failPersist: true}
	attempts := 0
	q.SetRunner(func(run context.Context, j *Job) error {
		if j.ID != en.Job.ID {
			return nil
		}
		attempts++
		if attempts <= 2 {
			return errors.New("checksum mismatch")
		}
		if attempts == 3 {
			if j.Step != store.FreshWordPressRollbackStep {
				t.Fatalf("missing rollback intent: %+v", j)
			}
			return errors.Join(ErrRetry, errors.New("injected teardown failure"))
		}
		if attempts == 4 {
			if err := s.MarkFreshWordPressRollbackCleaned(run, j.ID); err != nil {
				return err
			}
			j.Step = store.FreshWordPressRollbackCleanedStep
			return errors.Join(ErrRetry, store.ErrSitePrecondition)
		}
		if j.Step != store.FreshWordPressRollbackCleanedStep {
			t.Fatalf("lost cleaned checkpoint: %+v", j)
		}
		return rpcerr.Errf("agent_error", "installation failed")
	})
	if err = q.execute(ctx, en.Job); !errors.Is(err, ErrRetry) {
		t.Fatalf("intent write failure must retry: %v", err)
	}
	j, err := s.GetJob(ctx, en.Job.ID)
	if err != nil || j.State != "running" || j.Step != "" {
		t.Fatalf("failed intent must not requeue install: %+v %v", j, err)
	}
	if err = s.RecoverJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	j, err = s.GetJob(ctx, en.Job.ID)
	if err != nil || j.State != "queued" {
		t.Fatalf("recovery did not queue re-verification: %+v %v", j, err)
	}
	if err = q.execute(ctx, j); err != nil {
		t.Fatal(err)
	}
	j, err = s.GetJob(ctx, en.Job.ID)
	if err != nil || j.State != "queued" || j.Step != store.FreshWordPressRollbackIntentStep {
		t.Fatalf("intent refusal stopped worker: %+v %v", j, err)
	}
	otherBefore, err := q.Enqueue(ctx, "reconcile", map[string]string{}, "other-system")
	if err != nil {
		t.Fatal(err)
	}
	if err = q.execute(ctx, otherBefore.Job); err != nil {
		t.Fatal(err)
	}
	otherBeforeJob, err := s.GetJob(ctx, otherBefore.Job.ID)
	if err != nil || otherBeforeJob.State != "done" {
		t.Fatalf("intent refusal blocked unrelated work: %+v %v", otherBeforeJob, err)
	}
	// A crash after fencing retains the step even though RecoverJobs clears code.
	if _, err = s.StartJob(ctx, j.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	j, err = s.GetJob(ctx, en.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.execute(ctx, j); err != nil {
		t.Fatal(err)
	}
	j, err = s.GetJob(ctx, en.Job.ID)
	if err != nil || j.State != "queued" || j.Step != store.FreshWordPressRollbackStep {
		t.Fatalf("rollback not queued: %+v %v", j, err)
	}
	if err = q.execute(ctx, j); err != nil {
		t.Fatal(err)
	}
	j, err = s.GetJob(ctx, en.Job.ID)
	if err != nil || j.State != "queued" || j.Step != store.FreshWordPressRollbackStep {
		t.Fatalf("teardown failure lost intent: %+v %v", j, err)
	}
	// A crash after the next start leaves this running; startup must requeue it.
	if _, err = s.StartJob(ctx, j.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatalf("stuck rollback blocked startup: %v", err)
	}
	j, err = s.GetJob(ctx, en.Job.ID)
	if err != nil || j.State != "queued" || j.Step != store.FreshWordPressRollbackStep {
		t.Fatalf("restart lost rollback: %+v %v", j, err)
	}

	other, err := q.Enqueue(ctx, "reconcile", map[string]string{}, "system")
	if err != nil {
		t.Fatal(err)
	}
	if err = q.execute(ctx, other.Job); err != nil {
		t.Fatal(err)
	}
	otherJob, err := s.GetJob(ctx, other.Job.ID)
	if err != nil || otherJob.State != "done" {
		t.Fatalf("unrelated work blocked: %+v %v", otherJob, err)
	}
	if err = q.execute(ctx, j); err != nil {
		t.Fatal(err)
	}
	j, err = s.GetJob(ctx, en.Job.ID)
	if err != nil || j.State != "queued" || j.Step != store.FreshWordPressRollbackCleanedStep {
		t.Fatalf("custody failure lost retry: %+v %v", j, err)
	}
	if err = q.execute(ctx, j); err != nil {
		t.Fatal(err)
	}
	j, err = s.GetJob(ctx, en.Job.ID)
	if err != nil || j.State != "failed" {
		t.Fatalf("rollback not terminal: %+v %v", j, err)
	}
	if _, err = s.SiteByID(ctx, reservation.SiteID); err == nil {
		t.Fatal("failed site retained")
	}
	reservation.SiteID = "01K8WMXZF57CGNZKX1FZ38DZMC"
	reservation.DomainID = "01K8WMXZF57CGNZKX1FZ38DZMD"
	site.ID = reservation.SiteID
	domain.ID = reservation.DomainID
	replacement, err := q.EnqueueFreshWordPress(ctx, reservation, site, domain, store.PHPLimits{}, credentials)
	if err != nil {
		t.Fatal("same name not reusable", err)
	}
	op, err := s.LoadOperation(ctx, replacement.Job.ID)
	if err != nil || op.Identity.WordPress.Input.UID != 2000001 {
		t.Fatalf("UID history: %+v %v", op, err)
	}
	if err = s.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `UPDATE phase4_operations SET expires=1 WHERE job_id=?`, replacement.Job.ID)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatalf("fresh install blocked startup: %v", err)
	}
	q.SetRunner(func(_ context.Context, j *Job) error {
		if j.Step != "" {
			t.Fatal("TTL forced rollback of a job-driven install")
		}
		return ErrRetry
	})
	if err = q.execute(ctx, replacement.Job); err != nil {
		t.Fatal(err)
	}
	j, err = s.GetJob(ctx, replacement.Job.ID)
	if err != nil || j.State != "queued" || j.Step != "" {
		t.Fatalf("install did not remain recoverable: %+v %v", j, err)
	}
}
