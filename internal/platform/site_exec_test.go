package platform

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	runner "novapanel/internal/exec"
)

type siteExecFixture struct {
	calls  int
	child  int
	passwd string
	group  string
	err    error
	opts   runner.RunOpts
}

func (f *siteExecFixture) Run(ctx context.Context, prog string, args []string, o runner.RunOpts) ([]byte, []byte, int, error) {
	f.calls++
	if prog == "/usr/bin/getent" {
		if o.Uid != 0 || o.Gid != 0 || o.Stdin != nil || len(o.Env) != 0 {
			return nil, nil, -1, errors.New("lookup options")
		}
		if len(args) == 2 && args[0] == "passwd" && args[1] == "demo" {
			return []byte(f.passwd), nil, 0, f.err
		}
		if reflect.DeepEqual(args, []string{"passwd", "www-data"}) {
			return []byte("www-data:x:33:33::/var/www:/usr/sbin/nologin\n"), nil, 0, nil
		}
		if reflect.DeepEqual(args, []string{"group", "www-data"}) {
			return []byte(f.group), nil, 0, nil
		}
		return nil, nil, -1, errors.New("unexpected lookup")
	}
	f.child++
	f.opts = o
	return []byte("child"), nil, 0, nil
}
func newSiteExecFixture() *siteExecFixture {
	return &siteExecFixture{passwd: "demo:x:23451:23452::/srv/sites/demo:/usr/sbin/nologin\n", group: "www-data:x:33:\n"}
}
func TestSiteExecMatchesReservedIdentity(t *testing.T) {
	f := newSiteExecFixture()
	r, err := NewSiteRunner(context.Background(), f, "/srv/sites", "demo", 23451, 23452)
	if err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader("stdin-secret\n")
	out, _, code, err := r.Run(context.Background(), "/usr/bin/php8.3", []string{"fixed"}, runner.RunOpts{Dir: "/controlled", Env: []string{"ONLY=yes"}, Stdin: input, MaxOutput: 71})
	if err != nil || code != 0 || string(out) != "child" || f.child != 1 {
		t.Fatal("child not executed", err)
	}
	if f.opts.Uid != 23451 || f.opts.Gid != 23452 || f.opts.Groups == nil || len(f.opts.Groups) != 0 || f.opts.Dir != "/controlled" || !reflect.DeepEqual(f.opts.Env, []string{"ONLY=yes"}) || f.opts.Stdin != input || f.opts.MaxOutput != 71 {
		t.Fatal("execution contract changed")
	}
}

func TestSiteExecRefusesChangedOrUnsafeAccount(t *testing.T) {
	for _, change := range []string{
		"demo:x:0:23452::/srv/sites/demo:/usr/sbin/nologin\n",
		"demo:x:23451:0::/srv/sites/demo:/usr/sbin/nologin\n",
		"demo:x:23451:23452::/srv/sites/demo:/bin/bash\n",
		"demo:x:23451:23452::/other:/usr/sbin/nologin\n",
		"other:x:23451:23452::/srv/sites/demo:/usr/sbin/nologin\n",
		"demo:x:023451:23452::/srv/sites/demo:/usr/sbin/nologin\n",
		"demo:x:23451:23452::/srv/sites/demo:/usr/sbin/nologin\nextra",
		strings.Repeat("secret", 1000),
	} {
		f := newSiteExecFixture()
		r, e := NewSiteRunner(context.Background(), f, "/srv/sites", "demo", 23451, 23452)
		if e != nil {
			t.Fatal(e)
		}
		f.passwd = change
		out, stderr, code, e := r.Run(context.Background(), "/usr/bin/id", nil, runner.RunOpts{})
		if e == nil || f.child != 0 || code != -1 || len(out) != 0 || len(stderr) != 0 || strings.Contains(e.Error(), "secret") {
			t.Fatal("unsafe changed account admitted or disclosed")
		}
	}
}
func TestSiteExecRefusesMissingDependenciesAndOverrides(t *testing.T) {
	ctx := context.Background()
	for _, pair := range [][2]uint32{{0, 23452}, {23451, 0}, {33, 23452}, {23451, 33}} {
		f := newSiteExecFixture()
		_, e := NewSiteRunner(ctx, f, "/srv/sites", "demo", pair[0], pair[1])
		if e == nil || f.child != 0 {
			t.Fatal("unsafe reserved identity accepted")
		}
	}
	if _, e := NewSiteRunner(ctx, nil, "/srv/sites", "demo", 23451, 23452); e == nil {
		t.Fatal("missing runner accepted")
	}
	f := newSiteExecFixture()
	r, e := NewSiteRunner(ctx, f, "/srv/sites", "demo", 23451, 23452)
	if e != nil {
		t.Fatal(e)
	}
	for _, o := range []runner.RunOpts{{Uid: 1}, {Gid: 1}, {Groups: []uint32{33}}} {
		if _, _, _, e = r.Run(ctx, "/usr/bin/id", nil, o); e == nil {
			t.Fatal("override accepted")
		}
	}
	f.group = "www-data:x:23452:\n"
	if _, _, _, e = r.Run(ctx, "/usr/bin/id", nil, runner.RunOpts{}); e == nil {
		t.Fatal("changed nginx group accepted")
	}
	f.group = "www-data:x:33:\n"
	f.err = errors.New("resolver-secret")
	if _, _, _, e = r.Run(ctx, "/usr/bin/id", nil, runner.RunOpts{}); e == nil || strings.Contains(e.Error(), "resolver-secret") {
		t.Fatal("resolver failure leaked or ignored")
	}
	if f.child != 0 {
		t.Fatal("refused child ran")
	}
}

type siteExecRealFixture struct{ *siteExecFixture }

func (f siteExecRealFixture) Run(ctx context.Context, p string, a []string, o runner.RunOpts) ([]byte, []byte, int, error) {
	if p == "/usr/bin/getent" {
		return f.siteExecFixture.Run(ctx, p, a, o)
	}
	return (runner.Real{}).Run(ctx, p, a, o)
}
func TestSiteExecRealCredentialDropAndCancellation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root credential-drop fixture")
	}
	t.Setenv("NOVAPANEL_SITE_EXEC_CANARY", "must-not-inherit")
	r, e := NewSiteRunner(context.Background(), siteExecRealFixture{newSiteExecFixture()}, "/srv/sites", "demo", 23451, 23452)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{{[]string{"-u"}, "23451\n"}, {[]string{"-g"}, "23452\n"}, {[]string{"-G"}, "23452\n"}} {
		out, stderr, code, e := r.Run(context.Background(), "/usr/bin/id", tc.args, runner.RunOpts{})
		if e != nil || code != 0 || len(stderr) != 0 || string(out) != tc.want {
			t.Fatalf("real credentials %q %q %d %v", out, stderr, code, e)
		}
	}
	out, _, code, e := r.Run(context.Background(), "/usr/bin/env", nil, runner.RunOpts{})
	if e != nil || code != 0 || len(out) != 0 {
		t.Fatal("ambient environment inherited")
	}
	out, _, code, e = r.Run(context.Background(), "/usr/bin/cat", nil, runner.RunOpts{Stdin: strings.NewReader("stdin-only-secret\n")})
	if e != nil || code != 0 || string(out) != "stdin-only-secret\n" {
		t.Fatal("stdin contract changed")
	}
	out, _, _, e = r.Run(context.Background(), "/usr/bin/printf", []string{"%s", strings.Repeat("x", 1000)}, runner.RunOpts{MaxOutput: 7})
	if e != nil || len(out) != 7 {
		t.Fatal("output bound changed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, _, e = r.Run(ctx, "/usr/bin/sleep", []string{"30"}, runner.RunOpts{})
	if !errors.Is(e, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatal("cancellation lost", e)
	}
}
