//go:build linux

package jobs

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
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

// stageOfficialReadBarrier holds actual extraction in its final byte comparison.
// Only the private scratch directory is marked. Unlike a file lease, a fanotify
// permission event blocks even an O_NONBLOCK open/read. Closing the group releases
// pending events on every failure path. No production callback or pin is changed.
func stageOfficialReadBarrier(parent string, entered chan<- struct{}, release <-chan struct{}, done chan<- error) (func(), error) {
	fd, err := unix.FanotifyInit(unix.FAN_CLASS_CONTENT|unix.FAN_CLOEXEC|unix.FAN_NONBLOCK, unix.O_RDONLY|unix.O_CLOEXEC)
	if err != nil {
		return nil, err
	}
	if err = unix.FanotifyMark(fd, unix.FAN_MARK_ADD, unix.FAN_ACCESS_PERM|unix.FAN_EVENT_ON_CHILD, unix.AT_FDCWD, parent); err != nil {
		unix.Close(fd)
		return nil, err
	}
	var once sync.Once
	closeGroup := func() { once.Do(func() { unix.Close(fd) }) }
	go func() {
		var result error
		defer func() { done <- result }()
		buf := make([]byte, 4096)
		for {
			n, err := unix.Read(fd, buf)
			if err == unix.EAGAIN {
				select {
				case <-release:
					return
				case <-time.After(time.Millisecond):
					continue
				}
			}
			if err != nil {
				result = err
				return
			}
			for off := 0; off < n; {
				if n-off < 24 {
					result = errors.New("short fanotify event")
					return
				}
				event := buf[off:]
				length := int(binary.NativeEndian.Uint32(event))
				if length < 24 || length > n-off || event[4] != unix.FANOTIFY_METADATA_VERSION {
					result = errors.New("invalid fanotify event")
					return
				}
				off += length
				file := int(int32(binary.NativeEndian.Uint32(event[16:])))
				if file < 0 {
					result = errors.New("fanotify overflow")
					return
				}
				name, e := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", file))
				if e == nil && filepath.Base(name) == "version.php" {
					var st unix.Stat_t
					if e = unix.Fstat(file, &st); e == nil && st.Mode&07777 != 0444 {
						e = errors.New("extraction file was not completed before comparison")
					}
					if e == nil {
						entered <- struct{}{}
						<-release
					}
				}
				var response [8]byte
				binary.NativeEndian.PutUint32(response[:], uint32(file))
				binary.NativeEndian.PutUint32(response[4:], unix.FAN_ALLOW)
				_, writeErr := unix.Write(fd, response[:])
				unix.Close(file)
				if e != nil || writeErr != nil {
					result = errors.Join(e, writeErr)
					return
				}
			}
		}
	}()
	return closeGroup, nil
}

// These are original Queue.Run tests with real canonical bytes and SQLite/audit.
// Extraction rewrites a missing official file, then holds its final comparison.
// This does not claim a hard deadline for a blocked filesystem syscall.
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
				roots := []string{custody, filepath.Join(root, "stages-second")}
				if err := os.Mkdir(roots[1], 0700); err != nil {
					t.Fatal(err)
				}
				extractRelease := make(chan struct{})
				var extractOnce sync.Once
				releaseExtraction := func() { extractOnce.Do(func() { close(extractRelease) }) }
				extractEntered := make(chan struct{}, 2)
				var barriers []func()
				var barrierDone []chan error
				unlock := func() {
					for _, closeGroup := range barriers {
						closeGroup()
					}
				}
				// Release every fixture barrier and join Queue.Run even on an intentional RED.
				defer func() {
					cancel()
					end()
					unblock()
					releaseExtraction()
					for _, done := range barrierDone {
						stageOfficialWait(t, done)
					}
					unlock()
					stageOfficialWait(t, workerDone)
					stages.Wait()
				}()
				q.SetRunner(func(w context.Context, j *Job) error {
					if err := s.CheckpointOperation(w, j.ID, "prepared", "db_applied"); err != nil {
						return err
					}
					if boundary == "extraction" {
						for _, stageRoot := range roots {
							target := filepath.Join(stageRoot, j.ID, "tree", "wp-includes", "version.php")
							// Only empty directories are prepared. Every file, including
							// identity.json and version.php, is freshly written by StageWordPress.
							if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
								return err
							}
							done := make(chan error, 1)
							closeGroup, err := stageOfficialReadBarrier(filepath.Dir(target), extractEntered, extractRelease, done)
							if err != nil {
								return fmt.Errorf("scratch fanotify permission barrier requires CAP_SYS_ADMIN: %w", err)
							}
							barriers = append(barriers, closeGroup)
							barrierDone = append(barrierDone, done)
						}
					}
					retained <- context.WithoutCancel(w)
					for i := 0; i < 2; i++ {
						stages.Add(1)
						stageRoot := roots[i]
						go func() {
							defer stages.Done()
							stageDone <- q.StageWordPress(context.WithoutCancel(w), j.ID, stageRoot, p, verifier)
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
					// Both calls have actually rewritten version.php and reached a read
					// comparison of those completed official bytes before cancellation.
					stageOfficialWait(t, extractEntered)
					stageOfficialWait(t, extractEntered)
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
				// Cancel cases retain the running journal. Return/panic cases must
				// finish without queue cancellation and expire their sealed inputs.
				unblock()
				releaseExtraction()
				for _, done := range barrierDone {
					if err := stageOfficialWait(t, done); err != nil {
						t.Fatal("extraction barrier", err)
					}
					done <- nil
				}
				unlock()
				for i := 0; i < 2; i++ {
					if err := stageOfficialWait(t, stageDone); !errors.Is(err, context.Canceled) {
						t.Errorf("stage cancellation = %v", err)
					}
				}
				if ending != "cancel" {
					deadline := time.After(5 * time.Second)
					for {
						j, err = s.GetJob(ctx, en.Job.ID)
						if err != nil {
							t.Fatal(err)
						}
						if j.State == "failed" {
							break
						}
						select {
						case <-deadline:
							t.Fatal("uncanceled worker did not publish terminal failure")
						case <-time.After(time.Millisecond):
						}
					}
					op, err = s.LoadOperation(ctx, en.Job.ID)
					if err != nil || op.Checkpoint != "expired" || len(op.Ciphertext) != 0 {
						t.Fatal("terminal worker retained sealed inputs", err)
					}
				}
				cancel()
				workerErr := stageOfficialWait(t, workerDone)
				workerDone <- nil
				if workerErr != nil && (ending == "cancel" || !errors.Is(workerErr, ErrRecoveryBlocked)) {
					t.Fatal(workerErr)
				}
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
				if ending != "cancel" {
					unexpected := make(chan struct{}, 1)
					q.SetRunner(func(context.Context, *Job) error { unexpected <- struct{}{}; return errors.New("terminal replay") })
					restart, stop := context.WithTimeout(ctx, 300*time.Millisecond)
					defer stop()
					if err := q.Run(restart); err != nil && !errors.Is(err, ErrRecoveryBlocked) {
						t.Fatal("terminal restart", err)
					}
					select {
					case <-unexpected:
						t.Fatal("terminal job replayed after SQLite/audit reopen")
					default:
					}
					j, err = s.GetJob(ctx, en.Job.ID)
					if err != nil || j.State != "failed" {
						t.Fatal("terminal state changed on restart", err)
					}
					t.Log("canonical bytes, real gpgv, canceled drain, retained refusal, uncanceled terminal failure and SQLite/audit reopen no-replay passed; no PHP or live publication")
					return
				}
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
				// Recovery above revalidates the complete real official tree.
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
