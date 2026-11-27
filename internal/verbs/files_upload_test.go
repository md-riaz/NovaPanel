package verbs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"novapanel/internal/agent"
	runner "novapanel/internal/exec"
	"novapanel/internal/fileop"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

type uploadRunner struct{ root, siteID string }

func (r uploadRunner) Run(_ context.Context, program string, _ []string, opts runner.RunOpts) ([]byte, []byte, int, error) {
	if program == "/usr/bin/getent" {
		return []byte(fmt.Sprintf("demo:x:123:123:novapanel-site=%s:%s:/usr/sbin/nologin\n", r.siteID, filepath.Join(r.root, "demo"))), nil, 0, nil
	}
	if opts.Uid != 123 || opts.Gid != 123 || len(opts.Groups) != 0 {
		return nil, nil, 1, fmt.Errorf("wrong identity")
	}
	var req fileop.Request
	rd := bufio.NewReader(opts.Stdin)
	header, err := rd.ReadBytes('\n')
	if err != nil {
		return nil, nil, 1, err
	}
	if err := json.Unmarshal(header, &req); err != nil {
		return nil, nil, 1, err
	}
	data, err := io.ReadAll(rd)
	if err != nil || req.Operation != "upload" || len(data) != req.Length {
		return nil, nil, 1, fmt.Errorf("invalid stream")
	}
	if err := os.WriteFile(filepath.Join(req.Base, req.Name, "public", req.Path), data, 0600); err != nil {
		return nil, nil, 1, err
	}
	return []byte(`{}`), nil, 0, nil
}

func TestUploadExec(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sid, _ := id.New()
	did, _ := id.New()
	uploadID, _ := id.New()
	sites := filepath.Join(root, "sites")
	uploads := filepath.Join(root, "uploads")
	if err := os.MkdirAll(filepath.Join(sites, "demo", "public"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(uploads, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: filepath.Join(sites, "demo", "public")}, store.Domain{ID: did, FQDN: "demo.test"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	a := agent.New()
	a.Store = st
	a.Exec = uploadRunner{sites, sid}
	a.Cfg.SitesRoot = sites
	a.Cfg.UploadsDir = uploads
	a.Jobs = jobs.NewQueue(st, nil, nil)
	args := &filesUploadArgs{SiteID: sid, Path: "file", UploadID: uploadID}
	verb := rpc.Registry["files.upload"]
	if !verb.Slow || verb.ObjectKey(args) != "site:"+sid {
		t.Fatal("invalid registration")
	}
	raw, _ := json.Marshal(args)
	job := &jobs.Job{Args: raw}
	path := filepath.Join(uploads, uploadID)
	if err := os.WriteFile(path, []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	unlock, err := a.Jobs.LockSite(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	timed, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := filesUploadExec(timed, a, job); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(sites, "demo", "public", "file"))
	if err != nil || !bytes.Equal(data, []byte("payload")) || string(job.Result) != "{}" {
		t.Fatalf("result %q %s %v", data, job.Result, err)
	}
	check := func(want string) {
		t.Helper()
		err := filesUploadExec(timed, a, job)
		coded, ok := err.(*rpc.CodedError)
		if !ok || coded.Code != want {
			t.Fatalf("got %v, want %s", err, want)
		}
	}
	check("not_found")
	if err := os.Symlink(filepath.Join(sites, "demo", "public", "file"), path); err != nil {
		t.Fatal(err)
	}
	check("invalid_argument")
	os.Remove(path)
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	check("invalid_argument")
	os.Remove(path)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(fileop.MaxUpload + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	check("invalid_argument")
}
