// Package acme provides HTTP-01 issuance; durable policy and installation belong to the coordinator.
package acme

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
	"novapanel/internal/rpcerr"
	"novapanel/internal/validate"
)

var opaque = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var errChallenge = errors.New("invalid or unsafe ACME challenge")

// Webroot holds the challenge directory open. Its root must already exist,
// be agent-owned, world-traversable, and not writable by group or others.
// Callers serialize issuance per site and Close after lego has finished.
type Webroot struct {
	mu      sync.Mutex
	dir     *os.File
	domains map[string]bool
	active  map[string]map[string]string
}

func NewWebroot(root string, domains []string) (*Webroot, error) {
	if err := checkDomains(domains); err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || !filepath.IsAbs(root) || filepath.Clean(root) != root || resolved != root {
		return nil, errChallenge
	}
	fd, err := unix.Open(root, unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errChallenge
	}
	current := os.NewFile(uintptr(fd), root)
	for _, name := range []string{"", ".well-known", "acme-challenge"} {
		if name != "" {
			err = unix.Mkdirat(int(current.Fd()), name, 0755)
			if err != nil && !errors.Is(err, syscall.EEXIST) {
				current.Close()
				return nil, errChallenge
			}
			next, e := unix.Openat(int(current.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			current.Close()
			if e != nil {
				return nil, errChallenge
			}
			current = os.NewFile(uintptr(next), name)
		}
		st, e := current.Stat()
		if e != nil || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) || st.Mode().Perm()&0022 != 0 || st.Mode().Perm()&0005 != 0005 {
			current.Close()
			return nil, errChallenge
		}
	}
	p := &Webroot{dir: current, domains: make(map[string]bool), active: make(map[string]map[string]string)}
	for _, d := range domains {
		p.domains[d] = true
	}
	return p, nil
}

func validChallenge(token, auth string) bool {
	return len(token) >= 22 && len(token) <= 128 && opaque.MatchString(token) && len(auth) == len(token)+44 && strings.HasPrefix(auth, token+".") && opaque.MatchString(auth[len(token)+1:])
}

func (p *Webroot) Present(domain, token, keyAuth string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dir == nil || !p.domains[domain] || !validChallenge(token, keyAuth) {
		return errChallenge
	}
	if entries := p.active[token]; entries != nil {
		for _, auth := range entries {
			if auth != keyAuth {
				return errChallenge
			}
		}
		entries[domain] = keyAuth
		return nil
	}
	if len(p.active) >= 100 {
		return errChallenge
	}
	fd, err := unix.Openat(int(p.dir.Fd()), token, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0644)
	if err != nil {
		return errChallenge
	}
	f := os.NewFile(uintptr(fd), token)
	if err = f.Chmod(0644); err == nil {
		_, err = f.WriteString(keyAuth)
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil || ce != nil {
		unix.Unlinkat(int(p.dir.Fd()), token, 0)
		return errChallenge
	}
	p.active[token] = map[string]string{domain: keyAuth}
	return nil
}

func (p *Webroot) CleanUp(domain, token, keyAuth string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dir == nil || !p.domains[domain] || !validChallenge(token, keyAuth) {
		return errChallenge
	}
	entries := p.active[token]
	if entries == nil {
		return nil
	}
	if entries[domain] != keyAuth {
		return errChallenge
	}
	if len(entries) > 1 {
		delete(entries, domain)
		return nil
	}
	if err := unix.Unlinkat(int(p.dir.Fd()), token, 0); err != nil && !errors.Is(err, syscall.ENOENT) {
		return errChallenge
	}
	delete(p.active, token)
	return nil
}

func (p *Webroot) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dir == nil {
		return nil
	}
	var result error
	for token := range p.active {
		if err := unix.Unlinkat(int(p.dir.Fd()), token, 0); err != nil && !errors.Is(err, syscall.ENOENT) {
			result = errChallenge
		}
	}
	if err := p.dir.Close(); err != nil {
		result = errChallenge
	}
	p.dir = nil
	clear(p.active)
	return result
}

func checkDomains(domains []string) error {
	if len(domains) == 0 || len(domains) > 100 {
		return errors.New("invalid ACME domain set")
	}
	seen := make(map[string]bool)
	for _, d := range domains {
		if strings.Contains(d, "*") {
			return rpcerr.Errf("unsupported", "Wildcard ACME requires DNS-01; unsupported in Phase 3")
		}
		if (&validate.V{}).Hostname(d) != nil || net.ParseIP(d) != nil || !strings.Contains(d, ".") || seen[strings.ToLower(d)] {
			return errors.New("invalid ACME domain set")
		}
		seen[strings.ToLower(d)] = true
	}
	return nil
}
