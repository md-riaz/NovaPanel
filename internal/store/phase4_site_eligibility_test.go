package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestOperationCreatePublicationRechecksSite(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	site := "01K8WMXZF57CGNZKX1FZ38DZMP"
	phase4Exec(t, s.db.raw, `INSERT INTO sites(id,name,type,docroot,status,created,updated) VALUES('`+site+`','review','php','/srv/sites/review/public','active',1,1)`)
	j := Job{ID: "publication-site", Verb: "database.create", CreatedBy: "local-root", Role: "owner", State: "queued", Args: []byte(`{}`)}
	req := OperationRequest{Kind: DatabaseCreate, SiteID: site, DatabaseID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR"}
	if _, err = s.PrepareOperation(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "hash"}, req, []byte{1}, []byte{2}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartJob(ctx, j.ID, 1); err != nil {
		t.Fatal(err)
	}
	for _, step := range [][2]string{{"prepared", "db_applied"}, {"db_applied", "verified"}} {
		if err = s.CheckpointOperation(ctx, j.ID, step[0], step[1]); err != nil {
			t.Fatal(err)
		}
	}
	phase4Exec(t, s.db.raw, `UPDATE sites SET status='failed' WHERE id='`+site+`'`)
	if err = s.CompleteOperation(ctx, j.ID, &JobClaim{Nonce: []byte{3}, Ciphertext: []byte{4}}); !errors.Is(err, ErrSiteConflict) {
		t.Fatalf("publication eligibility: %v", err)
	}
	if _, err = s.GetDatabase(ctx, req.DatabaseID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("metadata published", err)
	}
	op, err := s.LoadOperation(ctx, j.ID)
	if err != nil || op.Checkpoint != "verified" || len(op.Ciphertext) == 0 {
		t.Fatal("journal changed", err)
	}
	if _, err = s.ConsumeClaim(ctx, j.ID, "hash", "local-root"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("claim published", err)
	}
}
