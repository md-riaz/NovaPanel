package verbs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"novapanel/internal/agent"
	"novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

func TestLogValidation(t *testing.T) {
	sid, _ := id.New()
	for _, a := range []logTailArgs{{Target: "unknown"}, {Target: "/etc/passwd"}, {Target: "../php"}, {Target: "php"}, {Target: "php", SiteID: "bad"}, {Target: "nginx", SiteID: sid}, {Target: "access"}, {Target: "error"}} {
		var ce *rpc.CodedError
		if err := a.Validate(); !errors.As(err, &ce) || ce.Code != "invalid_argument" {
			t.Fatalf("%+v: %v", a, err)
		}
	}
	for _, tc := range []struct{ in, want int }{{0, 200}, {-1, 200}, {1001, 1000}, {3, 3}} {
		a := logTailArgs{Target: "php", SiteID: sid, Lines: tc.in}
		if err := a.Validate(); err != nil || a.Lines != tc.want {
			t.Fatalf("%+v %v", a, err)
		}
	}
	v := rpc.Registry["log.tail"]
	if v.MinRole != rpc.Admin || v.ReadOnly || v.Scope != "sites:read" || v.Slow || v.Effectful || v.MFA || v.Exec != nil || v.NewJobArgs != nil {
		t.Fatal(v)
	}
}
func TestLogFileTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log")
	lines, err := tailLogFile(path, 2)
	if err != nil || lines == nil || len(lines) != 0 {
		t.Fatalf("%q %v", lines, err)
	}
	if err = os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0600); err != nil {
		t.Fatal(err)
	}
	lines, err = tailLogFile(path, 2)
	if err != nil || !reflect.DeepEqual(lines, []string{"two", "three"}) {
		t.Fatalf("%q %v", lines, err)
	}
	if err = os.WriteFile(path, []byte(strings.Repeat("x", logBytes+100)+"\nlast\n"), 0600); err != nil {
		t.Fatal(err)
	}
	lines, err = tailLogFile(path, 2)
	if err != nil || len(lines) != 2 || len(lines[0]) != logLineBytes || lines[1] != "last" {
		t.Fatalf("%v %v", len(lines), err)
	}
	hardlink := filepath.Join(dir, "hardlink")
	if err = os.Link(path, hardlink); err != nil {
		t.Fatal(err)
	}
	_, err = tailLogFile(hardlink, 2)
	var hardlinkErr *rpc.CodedError
	if !errors.As(err, &hardlinkErr) || hardlinkErr.Code != "invalid_argument" {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	_, err = tailLogFile(link, 2)
	var ce *rpc.CodedError
	if !errors.As(err, &ce) || ce.Code != "invalid_argument" {
		t.Fatal(err)
	}
}

type logRunner struct {
	t    *testing.T
	unit string
}

func (r logRunner) Run(_ context.Context, program string, args []string, opts exec.RunOpts) ([]byte, []byte, int, error) {
	r.t.Helper()
	want := []string{"--unit=" + r.unit, "--no-pager", "-n", "3", "--output=cat"}
	if program != "/usr/bin/journalctl" || !reflect.DeepEqual(args, want) || opts.MaxOutput != 4*(logLineBytes+128) {
		r.t.Fatalf("%q %q %+v", program, args, opts)
	}
	return []byte("first\nsecond\n"), nil, 0, nil
}
func TestLogJournal(t *testing.T) {
	for target, unit := range map[string]string{"nginx": "nginx.service", "php-fpm": "php8.3-fpm.service"} {
		a := &agent.Agent{Exec: logRunner{t, unit}}
		got, err := logTail(context.Background(), a, &logTailArgs{Target: target, Lines: 3})
		if err != nil || !reflect.DeepEqual(got, logTailReply{[]string{"first", "second"}}) {
			t.Fatalf("%v %v", got, err)
		}
	}
}

type largeJournalRunner struct{}

func (largeJournalRunner) Run(_ context.Context, _ string, _ []string, opts exec.RunOpts) ([]byte, []byte, int, error) {
	var output strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&output, "%03d%s\n", i, strings.Repeat("x", logLineBytes))
	}
	data := []byte(output.String())
	if len(data) > opts.MaxOutput {
		data = data[:opts.MaxOutput]
	}
	return data, nil, 0, nil
}

func TestLogJournalLargeTail(t *testing.T) {
	a := &agent.Agent{Exec: largeJournalRunner{}}
	got, err := logTail(context.Background(), a, &logTailArgs{Target: "nginx", Lines: 200})
	if err != nil {
		t.Fatal(err)
	}
	lines := got.(logTailReply).Lines
	if len(lines) != 200 {
		t.Fatalf("got %d lines", len(lines))
	}
	for i, line := range lines {
		if len(line) != logLineBytes || !strings.HasPrefix(line, fmt.Sprintf("%03d", i)) {
			t.Fatalf("line %d: length %d, prefix %.3q", i, len(line), line)
		}
	}
}

func TestPHPLogSiteIdentity(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sid, _ := id.New()
	did, _ := id.New()
	_, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, store.Domain{ID: did, FQDN: "demo.test"})
	if err != nil {
		t.Fatal(err)
	}
	site, err := st.SiteByID(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	path, err := phpLogPath(site.Name)
	if err != nil || path != "/var/log/novapanel/demo/php-error.log" {
		t.Fatalf("%q %v", path, err)
	}
}
