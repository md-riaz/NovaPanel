package jobs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	runner "novapanel/internal/exec"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
)

type runnerLifetimeBoundary struct {
	armed                      atomic.Bool
	child                      bool
	entered, canceled, release chan struct{}
	effects                    atomic.Int32
}

func (b *runnerLifetimeBoundary) Run(c context.Context, p string, a []string, o runner.RunOpts) ([]byte, []byte, int, error) {
	if p == "/usr/bin/getent" {
		if b.armed.Load() && !b.child && a[0] == "group" {
			close(b.entered)
			<-c.Done()
			close(b.canceled)
			<-b.release // Deliberately slow cancellation must retain locks and inputs.
		}
		return (reservedAccountFixture{}).Run(c, p, a, o)
	}
	b.effects.Add(1)
	o.Stdin = lifetimeStartedReader{c: c, started: b.entered}
	out, stderr, exit, err := (runner.Real{}).Run(c, p, a, o)
	close(b.canceled) // The real child has been waited, not only signaled.
	<-b.release
	return out, stderr, exit, err
}

type lifetimeStartedReader struct {
	c       context.Context
	started chan struct{}
}

func (r lifetimeStartedReader) Read([]byte) (int, error) {
	close(r.started) // internal/exec's stdin copier starts only after cmd.Start.
	<-r.c.Done()
	return 0, io.EOF
}

func TestWordPressSiteRunnerWorkerLifetime(t *testing.T) {
	for _, child := range []bool{false, true} {
		for _, ending := range []string{"return", "panic", "cancel"} {
			t.Run(map[bool]string{false: "lookup", true: "active-child"}[child]+"/"+ending, func(t *testing.T) {
				if child && os.Geteuid() != 0 {
					t.Skip("root credential-drop fixture")
				}
				c := WithMetadata(context.Background(), Metadata{CreatedBy: "session", Role: "owner", Effectful: true})
				s, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				seal, _ := secret.New(make([]byte, 32))
				q := NewQueue(s, seal, &fakeAudit{})
				if err = q.PrepareOperations(c); err != nil {
					t.Fatal(err)
				}
				m := wordpress.ApprovedArtifacts()
				res := store.WordPressReservation{Input: wordpress.InstallInput{SitesRoot: "/scratch/sites", SiteName: "example", PrimaryDomain: "example.test", UID: 23451, GID: 23452, Locale: "en_US", Administrator: wordpress.Administrator{Title: "Example", Username: "Admin", Email: "owner@example.test"}}, Evidence: wordpress.ArtifactEvidence{CLISHA256: m.CLI.SHA256, WordPressSHA256: m.WordPress.SHA256, ChecksumsSHA256: m.ChecksumsSHA256, SignatureVerified: true, SignerFingerprint: m.CLISignerFingerprint}}
				creds, _ := GenerateWordPressCredentials()
				en, err := q.EnqueueWordPress(c, res, creds)
				if err != nil {
					t.Fatal(err)
				}
				b := &runnerLifetimeBoundary{child: child, entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
				callDone := make(chan error, 1)
				workerDone := make(chan error, 1)
				var retained runner.Runner
				var original context.Context
				run, cancel := context.WithCancel(c)
				defer cancel()
				q.SetRunner(func(w context.Context, j *Job) error {
					r, e := q.WordPressSiteRunner(w, j.ID, b)
					if e != nil {
						return e
					}
					retained, original = r, w
					b.armed.Store(true)
					go func() {
						// WithoutCancel preserves lease values but must not detach worker lifetime.
						_, _, _, e := r.Run(context.WithoutCancel(w), map[bool]string{false: "/usr/bin/id", true: "/usr/bin/sleep"}[child], []string{"30"}, runner.RunOpts{})
						callDone <- e
					}()
					<-b.entered
					if ending == "cancel" {
						cancel()
						<-w.Done()
					}
					if ending == "panic" {
						panic("lifetime fixture")
					}
					return errors.New("worker returned")
				})
				go func() { workerDone <- q.execute(run, en.Job) }()
				select {
				case <-b.canceled:
				case <-time.After(5 * time.Second):
					close(b.release)
					t.Fatal("admitted invocation not canceled")
				}
				select {
				case e := <-workerDone:
					close(b.release)
					t.Fatalf("worker returned before drain: %v", e)
				default:
				}
				j, e := s.GetJob(c, en.Job.ID)
				if e != nil || j.State != "running" {
					t.Fatalf("terminal before drain: %s %v", j.State, e)
				}
				op, e := s.LoadOperation(c, en.Job.ID)
				if e != nil || len(op.Ciphertext) == 0 {
					t.Fatal("sealed input lost before drain", e)
				}
				// The complete operation-lock owner is still live while cancellation drains.
				q.locks.mu.Lock()
				held := len(q.locks.keys) > 0
				q.locks.mu.Unlock()
				if !held {
					t.Error("operation locks released before drain")
				}
				var wg sync.WaitGroup
				for i := 0; i < 16; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						if _, _, _, e := retained.Run(context.WithoutCancel(original), "/usr/bin/id", nil, runner.RunOpts{}); e == nil {
							t.Error("retained concurrent invocation accepted")
						}
					}()
				}
				wg.Wait()
				close(b.release)
				if e := <-callDone; e == nil {
					t.Error("canceled invocation succeeded")
				}
				if e := <-workerDone; e != nil {
					t.Fatal(e)
				}
				if got := b.effects.Load(); got != map[bool]int32{false: 0, true: 1}[child] {
					t.Errorf("unexpected child admissions: %d", got)
				}
				j, e = s.GetJob(c, en.Job.ID)
				want := "failed"
				if ending == "cancel" {
					want = "running"
				}
				if e != nil || j.State != want {
					t.Errorf("state=%s want=%s error=%v", j.State, want, e)
				}
			})
		}
	}
}
