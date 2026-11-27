package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestOperationExpiryBlocksUntilReconciled(t *testing.T) {
	ctx := context.Background()
	s, e := Open(filepath.Join(t.TempDir(), "panel.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	clock := &authClock{now: 1000}
	s.clock = clock
	j := Job{ID: "expiry-one", Verb: "database.create", CreatedBy: "owner-session", Role: "owner", State: "queued", Args: []byte(`{}`)}
	req := OperationRequest{Kind: DatabaseCreate, DatabaseID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR"}
	op, e := s.PrepareOperation(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "hash"}, req, []byte{1}, []byte{2})
	if e != nil {
		t.Fatal(e)
	}
	clock.now = op.Expires
	if e = s.CheckOperation(ctx, j.ID); !errors.Is(e, ErrOperationExpired) {
		t.Fatalf("expiry: %v", e)
	}
	if n, e := s.ExpireOperations(ctx); e != nil || n != 1 {
		t.Fatalf("expire %d %v", n, e)
	}
	op, e = s.LoadOperation(ctx, j.ID)
	if e != nil || op.Checkpoint != "expired" || len(op.Ciphertext) == 0 {
		t.Fatalf("expiry state %+v %v", op, e)
	}
	got, e := s.GetJob(ctx, j.ID)
	if e != nil || got.Code != "interrupted" {
		t.Fatalf("job %+v %v", got, e)
	}
	var claims int
	if e = s.Tx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT count(*) FROM job_claims WHERE job_id=?", j.ID).Scan(&claims)
	}); e != nil {
		t.Fatal(e)
	}
	if claims != 1 {
		t.Fatalf("expired operation lost claim reservation: %d", claims)
	}
	j.ID = "expiry-two"
	if _, e = s.PrepareOperation(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "hash"}, req, []byte{1}, []byte{2}); !errors.Is(e, ErrOperationLocked) {
		t.Fatalf("blocker lost: %v", e)
	}
	if e = s.ReconcileOperation(ctx, op.JobID); e == nil {
		t.Fatal("unresolved retained credential was reconciled")
	}
	if e = s.Tx(ctx, func(tx *sql.Tx) error { _, e := tx.Exec("DELETE FROM jobs WHERE id=?", op.JobID); return e }); e == nil {
		t.Fatal("journal job retention guard lost")
	}
}

func TestExpireOperationsLeavesFreshWordPressForJobRecovery(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.clock = &authClock{now: 1000}
	phase4Job(t, s.db.raw, "fresh-job", "queued")
	phase4Job(t, s.db.raw, "other-job", "queued")
	phase4Job(t, s.db.raw, "served-job", "queued")
	phase4Exec(t, s.db.raw, `INSERT INTO sites(id,name,type,docroot,created,updated,lifetime_state) VALUES('fresh','fresh','wordpress','/sites/fresh/public',1,1,'provisioning')`)
	phase4Exec(t, s.db.raw, `INSERT INTO site_account_provenance(site_id,account_name,uid,gid,allocating_job,evidence) VALUES('fresh','fresh',2000000,2000000,'fresh-job','reserved')`)
	phase4Exec(t, s.db.raw, `INSERT INTO sites(id,name,type,docroot,created,updated,lifetime_state) VALUES('served','served','wordpress','/sites/served/public',1,1,'served')`)
	phase4Exec(t, s.db.raw, `INSERT INTO site_account_provenance(site_id,account_name,uid,gid,allocating_job,evidence) VALUES('served','served',2000001,2000001,'served-job','reserved')`)
	phase4Exec(t, s.db.raw, `INSERT INTO phase4_operations(job_id,kind,site_id,checkpoint,identities_json,expires,updated) VALUES('served-job','wordpress_install','served','verified','{}',1,1)`)
	phase4Exec(t, s.db.raw, `INSERT INTO phase4_operations(job_id,kind,site_id,checkpoint,identities_json,expires,updated) VALUES('fresh-job','wordpress_install','fresh','db_applied','{}',1,1)`)
	phase4Exec(t, s.db.raw, `INSERT INTO phase4_operations(job_id,kind,checkpoint,identities_json,expires,updated) VALUES('other-job','database_create','prepared','{}',1,1)`)
	if n, err := s.ExpireOperations(ctx); err != nil || n != 1 {
		t.Fatalf("expiry count=%d err=%v", n, err)
	}
	if phase4Count(t, s.db.raw, `SELECT count(*) FROM phase4_operations o JOIN jobs j ON j.id=o.job_id WHERE o.job_id='fresh-job' AND o.checkpoint='db_applied' AND j.state='queued'`) != 1 {
		t.Fatal("fresh operation swept")
	}
	if phase4Count(t, s.db.raw, `SELECT count(*) FROM phase4_operations o JOIN jobs j ON j.id=o.job_id WHERE o.job_id='served-job' AND o.checkpoint='verified' AND j.state='queued'`) != 1 {
		t.Fatal("activated install swept")
	}
	if phase4Count(t, s.db.raw, `SELECT count(*) FROM phase4_operations o JOIN jobs j ON j.id=o.job_id WHERE o.job_id='other-job' AND o.checkpoint='expired' AND j.state='failed'`) != 1 {
		t.Fatal("unrelated expiry changed")
	}
	if n, err := s.ExpireOperations(ctx); err != nil || n != 0 {
		t.Fatalf("repeat expiry count=%d err=%v", n, err)
	}
	pending, err := s.PendingOperations(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatalf("fresh journal blocked startup: %v %v", pending, err)
	}

}

func TestFreshVerificationRecoveryDisposition(t *testing.T) {
	for _, tc := range []struct {
		checkpoint string
		rollback   bool
	}{
		{"db_applied", false}, {"verification_in_progress", true}, {"security_failed", true}, {"verified_resumable", false}, {"config_published", false}, {"verified", false},
	} {
		t.Run(tc.checkpoint, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(filepath.Join(t.TempDir(), "panel.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			phase4Job(t, s.db.raw, "fresh-job", "running")
			phase4Exec(t, s.db.raw, `INSERT INTO sites(id,name,type,docroot,created,updated,lifetime_state) VALUES('fresh','fresh','wordpress','/sites/fresh/public',1,1,'provisioning')`)
			phase4Exec(t, s.db.raw, `INSERT INTO site_account_provenance(site_id,account_name,uid,gid,allocating_job,evidence) VALUES('fresh','fresh',2000000,2000000,'fresh-job','reserved')`)
			phase4Exec(t, s.db.raw, `INSERT INTO phase4_operations(job_id,kind,site_id,checkpoint,identities_json,expires,updated) VALUES('fresh-job','wordpress_install','fresh',?,'{}',1,1)`, tc.checkpoint)
			if err = s.RecoverJobs(ctx, 2); err != nil {
				t.Fatal(err)
			}
			j, err := s.GetJob(ctx, "fresh-job")
			if err != nil || j.State != "queued" || (j.Step == FreshWordPressRollbackIntentStep) != tc.rollback {
				t.Fatalf("recovery: %+v %v", j, err)
			}
		})
	}
}
