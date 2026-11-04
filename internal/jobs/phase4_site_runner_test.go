package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	runner "novapanel/internal/exec"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
)

type reservedAccountFixture struct{ input wordpress.InstallInput }

func (f reservedAccountFixture) Run(ctx context.Context, p string, a []string, o runner.RunOpts) ([]byte, []byte, int, error) {
	if p == "/usr/bin/getent" {
		switch {
		case reflect.DeepEqual(a, []string{"passwd", "example"}):
			return []byte("example:x:23451:23452::/scratch/sites/example:/usr/sbin/nologin\n"), nil, 0, nil
		case reflect.DeepEqual(a, []string{"passwd", "www-data"}):
			return []byte("www-data:x:33:33::/var/www:/usr/sbin/nologin\n"), nil, 0, nil
		case reflect.DeepEqual(a, []string{"group", "www-data"}):
			return []byte("www-data:x:33:\n"), nil, 0, nil
		}
		return nil, nil, -1, errors.New("unexpected lookup")
	}
	return (runner.Real{}).Run(ctx, p, a, o)
}

// No PHP or DB effects here. Account lookups use an owned fixture; the id child
// and credential drop are real, and recovery uses the actual original Queue.Run.
func TestWordPressSiteRunnerOriginalQueueReopen(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root credential-drop fixture")
	}
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "session", Role: "owner", Effectful: true})
	path := filepath.Join(t.TempDir(), "panel.db")
	s, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	seal, _ := secret.New(make([]byte, 32))
	q := NewQueue(s, seal, &fakeAudit{})
	if e = q.PrepareOperations(ctx); e != nil {
		t.Fatal(e)
	}
	m := wordpress.ApprovedArtifacts()
	res := store.WordPressReservation{Input: wordpress.InstallInput{SitesRoot: "/scratch/sites", SiteName: "example", PrimaryDomain: "example.test", UID: 23451, GID: 23452, Locale: "en_US", Administrator: wordpress.Administrator{Title: "Example", Username: "Admin", Email: "owner@example.test"}}, Evidence: wordpress.ArtifactEvidence{CLISHA256: m.CLI.SHA256, WordPressSHA256: m.WordPress.SHA256, ChecksumsSHA256: m.ChecksumsSHA256, SignatureVerified: true, SignerFingerprint: m.CLISignerFingerprint}}
	credentials, e := GenerateWordPressCredentials()
	if e != nil {
		t.Fatal(e)
	}
	en, e := q.EnqueueWordPress(ctx, res, credentials)
	if e != nil {
		t.Fatal(e)
	}
	boundary := reservedAccountFixture{res.Input}
	if _, e = q.WordPressSiteRunner(ctx, en.Job.ID, boundary); e == nil {
		t.Fatal("outside worker accepted")
	}
	reached := make(chan error, 1)
	var retained runner.Runner
	q.SetRunner(func(c context.Context, j *Job) error {
		r, err := q.WordPressSiteRunner(c, j.ID, boundary)
		if err != nil {
			reached <- err
			return err
		}
		retained = r
		out, _, _, err := r.Run(c, "/usr/bin/id", []string{"-u"}, runner.RunOpts{})
		if err == nil && string(out) != "23451\n" {
			err = errors.New("wrong child identity")
		}
		if err == nil {
			err = s.CheckpointOperation(c, j.ID, "prepared", "db_applied")
		}
		reached <- err
		<-c.Done()
		return c.Err()
	})
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- q.Run(run) }()
	select {
	case e = <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("no worker")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if e != nil {
		t.Fatal(e)
	}
	if _, _, _, e = retained.Run(ctx, "/usr/bin/id", nil, runner.RunOpts{}); e == nil {
		t.Fatal("retained worker capability remained live")
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	q = NewQueue(s, seal, &fakeAudit{})
	reached = make(chan error, 1)
	q.SetRunner(func(c context.Context, j *Job) error {
		if j.ID != en.Job.ID {
			t.Error("replacement job")
		}
		got, err := q.OpenWordPressCredentials(c, j.ID)
		if err != nil {
			return err
		}
		if got != credentials {
			t.Error("original secrets changed")
		}
		op, err := s.LoadOperation(c, j.ID)
		if err != nil {
			return err
		}
		if op.Checkpoint != "db_applied" {
			t.Error("checkpoint lost")
		}
		r, err := q.WordPressSiteRunner(c, j.ID, boundary)
		if err == nil {
			_, _, _, err = r.Run(c, "/usr/bin/id", []string{"-G"}, runner.RunOpts{})
		}
		reached <- err
		<-c.Done()
		return c.Err()
	})
	run, cancel = context.WithCancel(ctx)
	go func() { done <- q.Run(run) }()
	select {
	case e = <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("reopen no worker")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if e != nil {
		t.Fatal(e)
	}
	op, e := s.LoadOperation(ctx, en.Job.ID)
	if e != nil || op.Checkpoint != "db_applied" || len(op.Ciphertext) == 0 {
		t.Fatal("adapter advanced installation or destroyed input", e)
	}
	if _, e = q.Claim(ctx, en.Job.ID, en.ClaimToken); e == nil {
		t.Fatal("premature claim")
	}
}
