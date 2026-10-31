package backup_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"novapanel/internal/audit"
	"novapanel/internal/backup"
	"novapanel/internal/jobs"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

type auditFixture struct{ events []audit.Event }

func (a *auditFixture) Record(e audit.Event) error { a.events = append(a.events, e); return nil }
func (*auditFixture) Healthy() error               { return nil }

func TestRepositoryPasswordUsesRealOwnerBoundOneShotClaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claims.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sealer, _ := secret.New(make([]byte, 32))
	log := &auditFixture{}
	q := jobs.NewQueue(s, sealer, log)
	owner := jobs.WithMetadata(context.Background(), jobs.Metadata{CreatedBy: "owner-session", Role: "owner", Actor: "owner"})
	enqueued, err := q.Enqueue(owner, "repo.create", map[string]any{"role": "panel"}, "repo:r")
	if err != nil {
		t.Fatal(err)
	}
	p, err := backup.GeneratePassword(sealer)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err = p.PublishClaim(owner, q, enqueued.Job.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	q = jobs.NewQueue(s, sealer, log)
	wrong := jobs.WithMetadata(context.Background(), jobs.Metadata{CreatedBy: "different-session", Role: "owner"})
	if _, err = q.Claim(wrong, enqueued.Job.ID, enqueued.ClaimToken); err == nil {
		t.Fatal("wrong owner claimed password")
	}
	fields, err := q.Claim(owner, enqueued.Job.ID, enqueued.ClaimToken)
	if err != nil {
		t.Fatal(err)
	}
	password, ok := fields["password"].(string)
	if !ok || len(password) < 32 {
		t.Fatal("missing password claim")
	}
	if _, err = q.Claim(owner, enqueued.Job.ID, enqueued.ClaimToken); err == nil {
		t.Fatal("claim replay accepted")
	}
	j, err := s.GetJob(owner, enqueued.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal([]any{j, log.events, p})
	if strings.Contains(string(encoded), password) || strings.Contains(fmt.Sprintf("%#v", p), password) {
		t.Fatal("secret leaked into result, audit or formatting")
	}
}
