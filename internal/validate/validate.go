// Package validate contains sink-specific allowlists. Failures are coded invalid_argument.
package validate

import (
	"net/netip"
	"net/url"
	"novapanel/internal/rpcerr"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

type V struct{ GitHosts []string }

func invalid(kind string) error { return rpcerr.Errf("invalid_argument", "invalid %s", kind) }

var label = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
var site = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
var db = regexp.MustCompile(`^[a-z][a-z0-9_]{1,46}$`)
var unit = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.@-]{0,239}\.(service|socket|timer|target|mount|path)$`)

var dnsLabel = regexp.MustCompile(`^[A-Za-z0-9_](?:[A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?$`)

// DNSOwner is for DNS records only, never web server names or host targets.
func (*V) DNSOwner(s string) error {
	if len(s) == 0 || len(s) > 253 {
		return invalid("DNS owner")
	}
	for _, l := range strings.Split(s, ".") {
		if !dnsLabel.MatchString(l) {
			return invalid("DNS owner")
		}
	}
	return nil
}

func (*V) Hostname(s string) error {
	if len(s) == 0 || len(s) > 253 {
		return invalid("hostname")
	}
	for _, l := range strings.Split(s, ".") {
		if !label.MatchString(l) {
			return invalid("hostname")
		}
	}
	return nil
}

var reserved = map[string]bool{"root": true, "daemon": true, "bin": true, "sys": true, "sync": true, "games": true, "man": true, "lp": true, "mail": true, "news": true, "uucp": true, "proxy": true, "www-data": true, "backup": true, "list": true, "irc": true, "nobody": true, "systemd-network": true, "systemd-timesync": true, "messagebus": true, "sshd": true, "mysql": true, "postgres": true, "redis": true, "nginx": true, "novapanel": true}

func (*V) SiteName(s string) error {
	if !site.MatchString(s) || reserved[s] {
		return invalid("site name")
	}
	return nil
}
func (v *V) UnixUser(s string) error { return v.SiteName(s) }
func (*V) DBName(s string) error {
	if !db.MatchString(s) || s == "mysql" || s == "sys" || s == "information_schema" || s == "performance_schema" {
		return invalid("database name")
	}
	return nil
}
func (*V) UnitName(s string) error {
	if !unit.MatchString(s) || strings.Contains(s, "..") {
		return invalid("unit name")
	}
	return nil
}
func traversal(s string) bool {
	for _, part := range strings.Split(filepath.ToSlash(s), "/") {
		if part == ".." {
			return true
		}
	}
	return strings.ContainsAny(s, "\\\x00\r\n")
}

// resolve permits not-yet-created descendants but evaluates every existing ancestor.
func resolve(p string) (string, error) {
	r, err := filepath.EvalSymlinks(p)
	if err == nil {
		return r, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	if _, e := os.Lstat(p); e == nil {
		return "", err
	} // dangling symlink must not become a future escape
	parent := filepath.Dir(p)
	if parent == p {
		return "", err
	}
	base, e := resolve(parent)
	if e != nil {
		return "", e
	}
	return filepath.Join(base, filepath.Base(p)), nil
}
func (*V) PathIn(root, p string) (string, error) {
	if root == "" || p == "" || traversal(p) {
		return "", invalid("path")
	}
	base, e := filepath.Abs(root)
	if e != nil {
		return "", invalid("root")
	}
	base, e = filepath.EvalSymlinks(base)
	if e != nil {
		return "", invalid("root")
	}
	dest := p
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(base, dest)
	}
	dest, e = resolve(filepath.Clean(dest))
	if e != nil {
		return "", invalid("path")
	}
	rel, e := filepath.Rel(base, dest)
	if e != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", invalid("path")
	}
	return dest, nil
}
func (v *V) ArchiveEntry(top, name string) error {
	if filepath.IsAbs(name) || traversal(name) {
		return invalid("archive entry")
	}
	_, e := v.PathIn(top, name)
	return e
}
func (v *V) GitURL(s string) error {
	u, e := url.Parse(s)
	decoded, decodeErr := url.PathUnescape(s)
	if e != nil || decodeErr != nil || unsafe(decoded) || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "ssh") || u.RawQuery != "" || u.Fragment != "" || unsafe(s) {
		return invalid("git URL")
	}
	if e = v.Hostname(u.Hostname()); e != nil {
		return invalid("git host")
	}
	if u.User != nil {
		if _, password := u.User.Password(); password {
			return invalid("git credentials")
		}
	}
	if len(v.GitHosts) > 0 {
		for _, h := range v.GitHosts {
			if strings.EqualFold(h, u.Hostname()) {
				return nil
			}
		}
		return invalid("git host")
	}
	return nil
}
func (*V) Port(n int) error {
	if n < 1 || n > 65535 {
		return invalid("port")
	}
	return nil
}
func (*V) CIDR(s string) error {
	if _, e := netip.ParsePrefix(s); e != nil {
		return invalid("CIDR")
	}
	return nil
}
func unsafe(s string) bool {
	return strings.ContainsAny(s, ";{}\\\"'`$") || strings.IndexFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0
}

// RedirectSource accepts a literal path or hostname, never an ACME challenge path.
func (v *V) RedirectSource(s string) error {
	if strings.HasPrefix(s, "/") {
		if strings.HasPrefix(s, "/.well-known") || strings.ContainsAny(s, "?#%") {
			return invalid("redirect source")
		}
		return v.RedirectTarget(s)
	}
	return v.Hostname(s)
}
func (*V) RedirectTarget(s string) error {
	decoded, e := url.PathUnescape(s)
	if e != nil || s == "" || unsafe(decoded) {
		return invalid("redirect target")
	}
	u, e := url.Parse(s)
	if e != nil {
		return invalid("redirect target")
	}
	if strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") && !strings.HasPrefix(decoded, "//") {
		return nil
	}
	if (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil {
		return nil
	}
	return invalid("redirect target")
}
