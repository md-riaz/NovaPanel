//go:build linux

package jobs

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"novapanel/internal/audit"
	runner "novapanel/internal/exec"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
)

// The barrier runs real gpgv first, then holds its return even after cancellation.
// It never supplies fabricated verification output or relaxes artifact custody.
type stageOfficialVerifier struct {
	entered chan context.Context
	release <-chan struct{}
}

func (b *stageOfficialVerifier) Run(ctx context.Context, prog string, args []string, o runner.RunOpts) ([]byte, []byte, int, error) {
	out, stderr, code, err := (runner.Real{}).Run(ctx, prog, args, o)
	if err != nil || code != 0 {
		return out, stderr, code, err
	}
	b.entered <- ctx
	<-b.release
	return out, stderr, code, err
}

func stageOfficialWait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(150 * time.Second):
		t.Fatal("official stage boundary timed out")
	}
	var zero T
	return zero
}

// These are original Queue.Run tests with real canonical bytes and SQLite/audit.
// The extraction case stalls a held-file comparison inside the real extractor;
// it does not claim a hard deadline for a blocked filesystem syscall.
func TestWordPressStageOfficialLifetime(t *testing.T) {
	artifacts := os.Getenv("NOVAPANEL_WP_STAGED_ARTIFACTS")
	if artifacts == "" {
		t.Skip("approved staged artifacts not supplied")
	}
	if os.Geteuid() != 0 {
		t.Skip("production stage custody requires root")
	}
	p := wordpress.StagedPaths{CLI: filepath.Join(artifacts, "cli.phar"), Signature: filepath.Join(artifacts, "cli.asc"), Keyring: filepath.Join(artifacts, "key.gpg"), Archive: filepath.Join(artifacts, "wordpress.zip"), Checksums: filepath.Join(artifacts, "checksums.json"), VerifierHome: filepath.Join(artifacts, "gnupg")}
	for _, boundary := range []string{"verification", "extraction"} {
		for _, ending := range []string{"return", "panic", "cancel"} {
			t.Run(boundary+"/"+ending, func(t *testing.T) {
				root := t.TempDir()
				custody := filepath.Join(root, "stages")
				if err := os.Mkdir(custody, 0700); err != nil {
					t.Fatal(err)
				}
				dbpath, auditpath := filepath.Join(root, "panel.db"), filepath.Join(root, "audit")
				s, err := store.Open(dbpath)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { s.Close() }()
				a, err := audit.Open(s, auditpath)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { a.Close() }()
				seal, err := secret.New(make([]byte, 32))
				if err != nil {
					t.Fatal(err)
				}
				q := NewQueue(s, seal, a)
				ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "session", Role: "owner", Effectful: true})
				if err = q.PrepareOperations(ctx); err != nil {
					t.Fatal(err)
				}
				evidence, err := wordpress.VerifyStaged(ctx, p, runner.Real{})
				if err != nil {
					t.Fatal(err)
				}
				credentials, err := GenerateWordPressCredentials()
				if err != nil {
					t.Fatal(err)
				}
				reservation := store.WordPressReservation{Input: wordpress.InstallInput{SitesRoot: "/scratch/sites", SiteName: "example", PrimaryDomain: "example.test", UID: 23451, GID: 23452, Locale: "en_US", Administrator: wordpress.Administrator{Title: "Example", Username: "Admin", Email: "owner@example.test"}}, Evidence: evidence}
				en, err := q.EnqueueWordPress(ctx, reservation, credentials)
				if err != nil {
					t.Fatal(err)
				}
				release := make(chan struct{})
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				verifier := &stageOfficialVerifier{entered: make(chan context.Context, 2), release: release}
				stageDone := make(chan error, 2)
				var stages sync.WaitGroup
				workerDone := make(chan error, 1)
				retained := make(chan context.Context, 1)
				started := make(chan struct{})
				endWorker := make(chan struct{})
				var endOnce sync.Once
				end := func() { endOnce.Do(func() { close(endWorker) }) }
				run, cancel := context.WithCancel(ctx)
				var lease *os.File
				var original os.FileInfo
				var signals chan os.Signal
				unlock := func() {
					if lease != nil {
						unix.FcntlInt(lease.Fd(), unix.F_SETLEASE, unix.F_UNLCK)
						lease.Close()
						lease = nil
					}
				}
				// Release every fixture barrier and join Queue.Run even on an intentional RED.
				defer func() {
					cancel()
					end()
					unblock()
					unlock()
					stageOfficialWait(t, workerDone)
					stages.Wait()
					if signals != nil {
						signal.Stop(signals)
					}
				}()
				q.SetRunner(func(w context.Context, j *Job) error {
					if err := s.CheckpointOperation(w, j.ID, "prepared", "db_applied"); err != nil {
						return err
					}
					if boundary == "extraction" {
						if err := q.StageWordPress(w, j.ID, custody, p, runner.Real{}); err != nil {
							return err
						}
						target := filepath.Join(custody, j.ID, "tree", "wp-includes", "version.php")
						var err error
						original, err = os.Stat(target)
						if err != nil {
							return err
						}
						lease, err = os.OpenFile(target, os.O_RDONLY, 0)
						if err != nil {
							return err
						}
						signals = make(chan os.Signal, 4)
						signal.Notify(signals, unix.SIGIO)
						if _, err = unix.FcntlInt(lease.Fd(), unix.F_SETOWN, os.Getpid()); err != nil {
							return err
						}
						if _, err = unix.FcntlInt(lease.Fd(), unix.F_SETLEASE, unix.F_WRLCK); err != nil {
							return err
						}
					}
					retained <- context.WithoutCancel(w)
					for i := 0; i < 2; i++ {
						stages.Add(1)
						go func() {
							defer stages.Done()
							stageDone <- q.StageWordPress(context.WithoutCancel(w), j.ID, custody, p, verifier)
						}()
					}
					close(started)
					<-endWorker
					if ending == "cancel" {
						<-w.Done()
					}
					if ending == "panic" {
						panic("official stage lifetime fixture")
					}
					return errors.New("official worker returned")
				})
				go func() { workerDone <- q.Run(run) }()
				select {
				case <-started:
				case err := <-workerDone:
					workerDone <- err
					t.Fatalf("worker failed before boundary: %v", err)
				case <-time.After(150 * time.Second):
					t.Fatal("worker did not start staging")
				}
				heldCtx := stageOfficialWait(t, retained)
				c1, c2 := stageOfficialWait(t, verifier.entered), stageOfficialWait(t, verifier.entered)
				if boundary == "extraction" {
					// Both admitted calls have verified every official byte and real signature.
					unblock()
					// A lease-break notification proves the real extractor attempted the file open.
					stageOfficialWait(t, signals)
				}
				if ending == "cancel" {
					cancel()
				}
				end()
				for _, call := range []context.Context{c1, c2} {
					select {
					case <-call.Done():
					case <-time.After(3 * time.Second):
						t.Fatal("official stage did not inherit worker cancellation")
					}
				}
				select {
				case err := <-workerDone:
					workerDone <- err
					t.Fatalf("worker finished before drain: %v", err)
				default:
				}
				j, err := s.GetJob(ctx, en.Job.ID)
				if err != nil || j.State != "running" {
					t.Fatalf("terminal before drain: %v %v", j, err)
				}
				op, err := s.LoadOperation(ctx, en.Job.ID)
				if err != nil || op.Checkpoint != "db_applied" || len(op.Ciphertext) == 0 {
					t.Fatal("sealed operation changed before drain", err)
				}
				q.locks.mu.Lock()
				held := len(q.locks.keys) > 0
				q.locks.mu.Unlock()
				if !held {
					t.Fatal("operation locks released before drain")
				}
				// Retained invocations race while the original admitted calls are draining.
				var refused sync.WaitGroup
				for i := 0; i < 4; i++ {
					refused.Add(1)
					go func() {
						defer refused.Done()
						if err := q.StageWordPress(heldCtx, en.Job.ID, custody, p, runner.Real{}); err == nil {
							t.Error("retained stage admitted during drain")
						}
					}()
				}
				refused.Wait()
				// Shutdown before releasing the filesystem barrier preserves the original
				// running journal for recovery, independent of terminal scheduling.
				cancel()
				unblock()
				unlock()
				for i := 0; i < 2; i++ {
					if err := stageOfficialWait(t, stageDone); !errors.Is(err, context.Canceled) {
						t.Errorf("stage cancellation = %v", err)
					}
				}
				// Return/panic can finish terminal handling; cancel stops the queue directly.
				cancel()
				if err := stageOfficialWait(t, workerDone); err != nil {
					t.Fatal(err)
				}
				// Keep the cleanup join idempotent after this successful receive.
				workerDone <- nil
				if err := q.StageWordPress(heldCtx, en.Job.ID, custody, p, runner.Real{}); err == nil {
					t.Fatal("retained stage admitted after worker exit")
				}
				if err = a.Close(); err != nil {
					t.Fatal(err)
				}
				if err = s.Close(); err != nil {
					t.Fatal(err)
				}
				s, err = store.Open(dbpath)
				if err != nil {
					t.Fatal(err)
				}
				a, err = audit.Open(s, auditpath)
				if err != nil {
					t.Fatal(err)
				}
				q = NewQueue(s, seal, a)
				reached := make(chan error, 1)
				q.SetRunner(func(w context.Context, j *Job) error {
					if j.ID != en.Job.ID {
						t.Error("replacement operation used")
					}
					err := q.StageWordPress(w, j.ID, custody, p, runner.Real{})
					reached <- err
					<-w.Done()
					return w.Err()
				})
				retry, stop := context.WithCancel(ctx)
				retryDone := make(chan error, 1)
				go func() { retryDone <- q.Run(retry) }()
				defer func() { stop(); stageOfficialWait(t, retryDone) }()
				if err := stageOfficialWait(t, reached); err != nil {
					t.Fatal("original-ID recovery failed", err)
				}
				stop()
				if err := stageOfficialWait(t, retryDone); err != nil {
					t.Fatal(err)
				}
				retryDone <- nil
				if original != nil {
					again, err := os.Stat(filepath.Join(custody, en.Job.ID, "tree", "wp-includes", "version.php"))
					if err != nil || !os.SameFile(original, again) {
						t.Fatal("recovery replaced extracted official file", err)
					}
				}
				op, err = s.LoadOperation(ctx, en.Job.ID)
				if err != nil || op.Checkpoint != "db_applied" || len(op.Ciphertext) == 0 {
					t.Fatal("staging advanced checkpoint or destroyed input", err)
				}
				j, err = s.GetJob(ctx, en.Job.ID)
				if err != nil || j.State == "done" {
					t.Fatal("staging published installation", err)
				}
				t.Log("canonical bytes, real gpgv, concurrent admitted stages, canceled drain, retained refusal and original Queue.Run SQLite/audit reopen passed; DB checkpoint injected; no PHP or live publication")
			})
		}
	}
}
