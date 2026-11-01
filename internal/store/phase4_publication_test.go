package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestPublicationPendingProjectionPrecedesPagination(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ids := []string{"01K8WMXZF57CGNZKX1FZ38DZMA", "01K8WMXZF57CGNZKX1FZ38DZMB", "01K8WMXZF57CGNZKX1FZ38DZMC"}
	for _, jid := range ids {
		j := Job{ID: jid, Verb: "database.create", ObjectKey: "db:x", CreatedBy: "local-root", Role: "owner", Args: json.RawMessage(`{}`), State: "done", Step: "phase4_committed", Progress: 1, Result: json.RawMessage(`{"db_id":"safe"}`), Error: "diagnostic", Code: "conflict"}
		if err = s.CreateJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	ident := OperationIdentity{Version: 1, Database: Database{ID: "01K8WMXZF57CGNZKX1FZ38DZMQ", Name: "d_01k8wmxzf57cgnzkx1fz38dzmq", Engine: "mariadb", PrimaryUserID: "01K8WMXZF57CGNZKX1FZ38DZMR"}, Accounts: []OperationAccount{{ID: "01K8WMXZF57CGNZKX1FZ38DZMR", Username: "u_01k8wmxzf57cgnzkx1fz38dzmr", Host: "127.0.0.1"}}}
	// Fixture persisted by an atomic terminal transaction interrupted before final audit.
	raw, _ := json.Marshal(ident)
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	fields["PublicationAudit"] = map[string]any{"Version": 1, "IntentEventID": "01K8WMXZF57CGNZKX1FZ38DZMS", "FinalEventID": "01K8WMXZF57CGNZKX1FZ38DZMT", "State": "committed"}
	raw, _ = json.Marshal(fields)
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `INSERT INTO phase4_operations(job_id,kind,db_id,checkpoint,identities_json,expires,updated) VALUES(?,'database_create',?,'done',?,1000,1)`, ids[2], ident.Database.ID, string(raw))
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetJob(ctx, ids[2]); err == nil {
		t.Fatal("pending status disclosed terminal success")
	}
	rows, err := s.ListJobs(ctx, JobFilter{State: "running", Limit: 1})
	if err != nil || len(rows) != 1 || rows[0].ID != ids[2] {
		t.Fatalf("running projection: %+v %v", rows, err)
	}
	j := rows[0]
	if j.State != "running" || j.Step != "publication_pending" || j.Progress != 0 || len(j.Result) != 0 || j.Error != "" || j.Code != "" || j.Finished != nil {
		t.Fatalf("projection leaked: %+v", j)
	}
	rows, err = s.ListJobs(ctx, JobFilter{State: "done", Limit: 1})
	if err != nil || len(rows) != 1 || rows[0].ID != ids[1] || rows[0].Error != "diagnostic" || rows[0].Code != "conflict" {
		t.Fatalf("unrelated diagnostics/pagination: %+v %v", rows, err)
	}
	rows, err = s.ListJobs(ctx, JobFilter{Before: ids[2], Limit: 1})
	if err != nil || len(rows) != 1 || rows[0].ID != ids[1] {
		t.Fatalf("cursor: %+v %v", rows, err)
	}
	if err = s.PutClaim(ctx, JobClaim{JobID: ids[2], ClaimHash: "hash", Nonce: []byte{1}, Ciphertext: []byte{2}, Expires: 9999999999999}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConsumeClaim(ctx, ids[2], "hash", "local-root"); err == nil || errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("pending claim not gated: %v", err)
	}
	var n int
	if err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM job_claims WHERE job_id=?", ids[2]).Scan(&n); err != nil || n != 1 {
		t.Fatalf("claim consumed: %d %v", n, err)
	}
}
