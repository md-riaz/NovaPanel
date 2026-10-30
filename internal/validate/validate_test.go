package validate

import (
	"errors"
	"novapanel/internal/rpcerr"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidators(t *testing.T) {
	v := &V{}
	cases := []struct {
		name      string
		f         func(string) error
		good, bad []string
	}{
		{"hostname", v.Hostname, []string{"example.com", "xn--bcher-kva.example"}, []string{"", "a..b", "a_b", "*.example.com", "x;evil", "-a.com", "a.", strings.Repeat("x", 64)}},
		{"site", v.SiteName, []string{"ab", "site-1"}, []string{"root", "a", "a_b", "a;id", "UPPER", "novapanel"}},
		{"user", v.UnixUser, []string{"web-1"}, []string{"root", "daemon", "nobody", "a\n"}},
		{"db", v.DBName, []string{"db_1"}, []string{"a", "db-name", "mysql", "information_schema"}},
		{"unit", v.UnitName, []string{"nginx.service", "php8.3-fpm.service"}, []string{"../nginx.service", "a;id.service", "-bad.service", "x\n.service"}},
		{"git", v.GitURL, []string{"https://github.com/user/repo.git", "ssh://git@example.com/repo"}, []string{"file:///etc/passwd", "ext::evil", "git@example.com:x", "https://x/a\n", "https:///x", "https://x/repo?x=1", "https://x/repo%0aevil", "https://x/repo%00evil"}},
		{"cidr", v.CIDR, []string{"127.0.0.0/8", "::1/128"}, []string{"127.0.0.1", "0/0", "x;id"}},
		{"redirect", v.RedirectTarget, []string{"/hello?q=1", "https://example.com/a"}, []string{"//evil.com", "javascript:evil", "/hello world", "https://x/\r\nX:yes", "/x;evil", "/x%0d%0aInjected"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, s := range c.good {
				if e := c.f(s); e != nil {
					t.Errorf("good %q: %v", s, e)
				}
			}
			for _, s := range c.bad {
				var e *rpcerr.CodedError
				if err := c.f(s); !errors.As(err, &e) || e.Code != "invalid_argument" {
					t.Errorf("bad %q: %v", s, err)
				}
			}
		})
	}
	for _, n := range []int{0, -1, 65536} {
		if v.Port(n) == nil {
			t.Errorf("port %d", n)
		}
	}
	if v.Port(443) != nil {
		t.Fatal("443")
	}
}
func TestPathConfinement(t *testing.T) {
	v := &V{}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../outside", outside, "link/new", "a/../b"} {
		if _, e := v.PathIn(root, p); e == nil {
			t.Errorf("accepted %q", p)
		}
	}
	got, e := v.PathIn(root, "new/nested")
	if e != nil || got != filepath.Join(root, "new/nested") {
		t.Fatalf("%s %v", got, e)
	}
	for _, p := range []string{"../evil", "/evil", "a/../../evil", "a\\..\\evil", "a/../b"} {
		if v.ArchiveEntry(root, p) == nil {
			t.Errorf("archive %q", p)
		}
	}
	if e := v.ArchiveEntry(root, "safe/file"); e != nil {
		t.Fatal(e)
	}
}
