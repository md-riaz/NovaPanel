package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestPublicationRequiresIntentAndFinalAcknowledgement(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	jid := "01K8WMXZF57CGNZKX1FZ38DZMA"
	j := Job{ID: jid, Verb: "database.create", ObjectKey: "db:x", CreatedBy: "local-root", Role: "owner", Args: []byte(`{}`), State: "queued"}
	r := OperationRequest{Kind: DatabaseCreate, DatabaseID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR"}
	if _, err = s.PrepareOperation(ctx, j, JobClaim{JobID: jid, ClaimHash: "hash"}, r, []byte{1}, []byte{2}); err != nil {
		t.Fatal(err)
	}
	s.StartJob(ctx, jid, 1)
	s.CheckpointOperation(ctx, jid, "prepared", "db_applied")
	s.CheckpointOperation(ctx, jid, "db_applied", "verified")
	intent, final := "01K8WMXZF57CGNZKX1FZ38DZMS", "01K8WMXZF57CGNZKX1FZ38DZMT"
	p, err := s.PreparePublicationAudit(ctx, jid, intent, final)
	if err != nil {
		t.Fatal(err)
	}
	if p.State != "intent" {
		t.Fatalf("intent: %+v", p)
	}
	claim := &JobClaim{Nonce: []byte{3}, Ciphertext: []byte{4}}
	if err = s.CompleteOperation(ctx, jid, claim); err == nil {
		t.Fatal("unmirrored intent accepted")
	}
	put := func(eid, publication string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"job_id": jid, "stage": "exec", "publication": publication})
		if e := s.InsertEvent(ctx, Event{ID: eid, Verb: j.Verb, ObjectKey: j.ObjectKey, Role: j.Role, Outcome: "ok", Detail: raw}); e != nil {
			t.Fatal(e)
		}
	}
	put(intent, "intent")
	if err = s.CompleteOperation(ctx, jid, claim); err != nil {
		t.Fatal(err)
	}
	op, err := s.LoadOperation(ctx, jid)
	if err != nil || op.Identity.PublicationAudit.State != "committed" {
		t.Fatalf("commit: %+v %v", op, err)
	}
	if _, err = s.GetJob(ctx, jid); err != ErrPublicationPending {
		t.Fatalf("status: %v", err)
	}
	if err = s.AcknowledgePublicationAudit(ctx, jid); err == nil {
		t.Fatal("missing final accepted")
	}
	put(final, "committed")
	if err = s.AcknowledgePublicationAudit(ctx, jid); err != nil {
		t.Fatal(err)
	}
	if got, e := s.GetJob(ctx, jid); e != nil || got.State != "done" {
		t.Fatalf("ack status: %+v %v", got, e)
	}
	if _, err = s.ConsumeClaim(ctx, jid, "hash", "local-root"); err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteOperation(ctx, jid, claim); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConsumeClaim(ctx, jid, "hash", "local-root"); err != sql.ErrNoRows {
		t.Fatalf("claim renewed: %v", err)
	}
}
