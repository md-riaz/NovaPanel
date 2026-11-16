package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	runner "novapanel/internal/exec"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
)

type stageLifetimeStore struct {
	*store.Store
	mu                         sync.Mutex
	armed                      bool
	entered, canceled, release chan struct{}
}

func (s *stageLifetimeStore) CheckOperation(ctx context.Context, id string) error {
	s.mu.Lock()
	armed := s.armed
	if armed {
		s.armed = false
	}
	s.mu.Unlock()
	if armed {
		close(s.entered)
		<-ctx.Done()
		close(s.canceled)
		<-s.release
		return ctx.Err()
	}
	return s.Store.CheckOperation(ctx, id)
}

func TestWordPressStageWorkerLifetime(t *testing.T) {
	for _, ending := range []string{"return", "panic", "cancel"} {
		t.Run(ending, func(t *testing.T) {
			ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "session", Role: "owner", Effectful: true})
			s, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			b := &stageLifetimeStore{Store: s, entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
			var release sync.Once
			defer release.Do(func() { close(b.release) })
			seal, _ := secret.New(make([]byte, 32))
			q := NewQueue(b, seal, &fakeAudit{})
			if err = q.PrepareOperations(ctx); err != nil {
				t.Fatal(err)
			}
			m := wordpress.ApprovedArtifacts()
			reservation := store.WordPressReservation{Input: wordpress.InstallInput{SitesRoot: "/scratch/sites", SiteName: "example", PrimaryDomain: "example.test", UID: 23451, GID: 23452, Locale: "en_US", Administrator: wordpress.Administrator{Title: "Example", Username: "Admin", Email: "owner@example.test"}}, Evidence: wordpress.ArtifactEvidence{CLISHA256: m.CLI.SHA256, WordPressSHA256: m.WordPress.SHA256, ChecksumsSHA256: m.ChecksumsSHA256, SignatureVerified: true, SignerFingerprint: m.CLISignerFingerprint}}
			credentials, _ := GenerateWordPressCredentials()
			en, err := q.EnqueueWordPress(ctx, reservation, credentials)
			if err != nil {
				t.Fatal(err)
			}
			workerDone := make(chan error, 1)
			stageDone := make(chan error, 1)
			run, cancel := context.WithCancel(ctx)
			defer cancel()
			q.SetRunner(func(w context.Context, j *Job) error {
				b.mu.Lock()
				b.armed = true
				b.mu.Unlock()
				go func() {
					stageDone <- q.StageWordPress(context.WithoutCancel(w), j.ID, "/missing", wordpress.StagedPaths{}, runner.Real{})
				}()
				<-b.entered
				if ending == "cancel" {
					cancel()
					<-w.Done()
				}
				if ending == "panic" {
					panic("stage lifetime fixture")
				}
				return errors.New("worker returned")
			})
			go func() { workerDone <- q.execute(run, en.Job) }()
			select {
			case <-b.canceled:
			case <-time.After(2 * time.Second):
				// Retain RED without leaking a deliberately blocked fixture goroutine.
				release.Do(func() { close(b.release) })
				t.Fatal("stage did not inherit worker cancellation")
			}
			select {
			case err := <-workerDone:
				t.Fatalf("worker finished before stage drain: %v", err)
			default:
			}
			j, err := s.GetJob(ctx, en.Job.ID)
			if err != nil || j.State != "running" {
				t.Fatalf("terminal before drain: %v %v", j, err)
			}
			op, err := s.LoadOperation(ctx, en.Job.ID)
			if err != nil || len(op.Ciphertext) == 0 {
				t.Fatal("sealed input lost before drain", err)
			}
			q.locks.mu.Lock()
			held := len(q.locks.keys) > 0
			q.locks.mu.Unlock()
			if !held {
				t.Fatal("locks released before stage drain")
			}
			release.Do(func() { close(b.release) })
			select {
			case err := <-stageDone:
				if err == nil {
					t.Error("canceled stage succeeded")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("stage not drained")
			}
			select {
			case err := <-workerDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("worker not drained")
			}
		})
	}
}
