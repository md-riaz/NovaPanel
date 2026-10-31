package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"novapanel/internal/audit"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"os"
	"strings"
	"time"
)

func TestOperationAdmissionRequiresStartup(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seal, _ := secret.New(make([]byte, 32))
	q := NewQueue(s, seal, &fakeAudit{})
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "local-root", Role: "owner"})
	_, err = q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.DatabaseCreate}, map[string]any{"password": "password"})
	if !errors.Is(err, ErrRecoveryBlocked) {
		t.Fatalf("admission before startup: %v", err)
	}
	rows, err := s.ListJobs(ctx, store.JobFilter{})
	if err != nil || len(rows) != 0 {
		t.Fatalf("blocked admission persisted jobs: %d %v", len(rows), err)
	}
}

func TestOperationRejectsUnknownRecoveryFields(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seal, _ := secret.New(make([]byte, 32))
	q := NewQueue(s, seal, &fakeAudit{})
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "owner-session", Role: "owner"})
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.DatabaseCreate}, map[string]any{"password": "secret", "unexpected": "not-a-contract"}); err == nil {
		t.Fatal("arbitrary recovery fields accepted")
	}
}

func TestOperationRestartRestoresStoredAuthorityAndFileAudit(t *testing.T) {
	for _, kind := range []store.OperationKind{store.DatabaseCreate, store.DatabaseRotate, store.DBUserCreate} {
		t.Run(string(kind), func(t *testing.T) {
			for _, authority := range []Metadata{
				{CreatedBy: "local-root", Role: "owner", Actor: "root-label"},
				{CreatedBy: "gone-session", Role: "owner", Actor: "user-label", Automation: true},
				{CreatedBy: "gone-token", Role: "token", Actor: "token-label", Scopes: []string{"databases:write"}, Automation: true},
			} {
				t.Run(authority.CreatedBy, func(t *testing.T) {
					ctx := WithMetadata(context.Background(), authority)
					dir := t.TempDir()
					path := filepath.Join(dir, "db")
					logpath := filepath.Join(dir, "audit")
					s, err := store.Open(path)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { s.Close() }()
					log, err := audit.Open(s, logpath)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { log.Close() }()
					seal, _ := secret.New(make([]byte, 32))
					q := NewQueue(s, seal, log)
					if err = q.PrepareOperations(ctx); err != nil {
						t.Fatal(err)
					}
					req := store.OperationRequest{Kind: kind}
					if kind != store.DatabaseCreate {
						pair := store.DatabasePrimary{ID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR"}
						if err = s.CreateDatabasePrimary(ctx, pair); err != nil {
							t.Fatal(err)
						}
						req.DatabaseID = pair.ID
					}
					r, err := q.EnqueueOperation(ctx, req, map[string]any{"password": "restart-only-password"})
					if err != nil {
						t.Fatal(err)
					}
					run, cancel := context.WithCancel(ctx)
					q.SetRunner(func(c context.Context, j *Job) error {
						if err := s.CheckpointOperation(c, j.ID, "prepared", "db_applied"); err != nil {
							return err
						}
						cancel()
						return context.Canceled
					})
					if err = q.Run(run); err != nil {
						t.Fatal(err)
					}
					if err = log.Close(); err != nil {
						t.Fatal(err)
					}
					if err = s.Close(); err != nil {
						t.Fatal(err)
					}
					s, err = store.Open(path)
					if err != nil {
						t.Fatal(err)
					}
					log, err = audit.Open(s, logpath)
					if err != nil {
						t.Fatal(err)
					}
					q = NewQueue(s, seal, log)
					q.SetRunner(func(c context.Context, j *Job) error {
						got, _ := MetadataFrom(c)
						if got.CreatedBy != authority.CreatedBy || got.Role != authority.Role || got.Automation != authority.Automation {
							t.Errorf("authority changed: %+v", got)
						}
						credentials, e := q.OpenOperationCredentials(c, j.ID)
						if e != nil || credentials.Password != "restart-only-password" {
							return errors.New("credential recovery failed")
						}
						return s.CheckpointOperation(c, j.ID, "db_applied", "verified")
					})
					// The startup context is deliberately a different, more privileged caller.
					run, cancel = context.WithCancel(WithMetadata(context.Background(), Metadata{CreatedBy: "current-owner", Role: "owner", Automation: true}))
					done := make(chan error, 1)
					go func() { done <- q.Run(run) }()
					deadline := time.After(3 * time.Second)
					for {
						j, e := s.GetJob(ctx, r.Job.ID)
						if e != nil {
							t.Fatal(e)
						}
						if j.State == "done" {
							break
						}
						select {
						case e := <-done:
							t.Fatalf("worker stopped: %v", e)
						case <-deadline:
							t.Fatal("worker timeout")
						case <-time.After(time.Millisecond):
						}
					}
					cancel()
					if err = <-done; err != nil {
						t.Fatal(err)
					}
					wrong := WithMetadata(context.Background(), Metadata{CreatedBy: "current-owner", Role: "owner"})
					if _, err = q.Claim(wrong, r.Job.ID, r.ClaimToken); err == nil {
						t.Fatal("current owner stole claim")
					}
					got, err := q.Claim(ctx, r.Job.ID, r.ClaimToken)
					if err != nil || got["password"] != "restart-only-password" || len(got) != 1 {
						t.Fatalf("claim: %v %v", got, err)
					}
					if _, err = q.Claim(ctx, r.Job.ID, r.ClaimToken); err == nil {
						t.Fatal("claim repeated")
					}
					data, err := os.ReadFile(logpath)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(data), "restart-only-password") || strings.Contains(string(data), "current-owner") {
						t.Fatal("audit leaked credential or current identity")
					}
					if !strings.Contains(string(data), `"stage":"exec"`) {
						t.Fatal("completion audit absent")
					}
					if authority.CreatedBy != "local-root" && !strings.Contains(string(data), authority.CreatedBy) {
						t.Fatal("stored principal missing from audit")
					}
				})
			}

		})
	}
}
