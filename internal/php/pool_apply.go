package php

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	runner "novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/rpcerr"
	"novapanel/internal/store"
	"novapanel/internal/validate"
)

// PoolApplier manages the shared FPM configuration. Dir and all its ancestors
// must be administrator-owned. Callers serialize site lifecycle operations and
// verify site ownership before removing or replacing a pool.
type PoolApplier struct {
	Dir    string
	Runner runner.Runner
	Probe  func(context.Context) error
	mu     sync.Mutex
}

// PoolState is a projection of committed SQLite state. Exists false represents
// the absence of a pool; callers must derive it under the site lifecycle lock.
type PoolState struct {
	SiteID      string
	Exists      bool
	MaxChildren int
	Limits      store.PHPLimits
}

// Legacy entry points cannot safely infer committed state from a derived file.
func (a *PoolApplier) Apply(ctx context.Context, name string, maxChildren int) error {
	return rpcerr.Errf("agent_error", "committed PHP state required")
}
func (a *PoolApplier) ApplyLimits(ctx context.Context, name string, maxChildren int, limits store.PHPLimits) error {
	return rpcerr.Errf("agent_error", "committed PHP state required")
}
func (a *PoolApplier) Remove(ctx context.Context, name string) error {
	return rpcerr.Errf("agent_error", "committed PHP state required")
}

func renderState(name string, state PoolState) ([]byte, error) {
	if err := (&validate.V{}).SiteName(name); err != nil {
		return nil, err
	}
	if !regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Z]{25}$`).MatchString(state.SiteID) {
		return nil, rpcerr.Errf("invalid_argument", "invalid site ID")
	}
	if !state.Exists {
		return nil, nil
	}
	b, err := RenderPoolLimits(name, state.MaxChildren, state.Limits)
	if err != nil {
		return nil, err
	}
	return append([]byte("; novapanel-managed site="+state.SiteID+"\n"), b...), nil
}

// ApplyCommitted tests the candidate before publishing it. On failure it restores
// the caller's committed SQLite projection, never the previous disk contents.
func (a *PoolApplier) ApplyCommitted(ctx context.Context, name string, desired, committed PoolState) error {
	if desired.SiteID != committed.SiteID {
		return rpcerr.Errf("invalid_argument", "site IDs differ")
	}
	b, err := renderState(name, desired)
	if err != nil {
		return err
	}
	old, err := renderState(name, committed)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.apply(ctx, name, desired.SiteID, b, old, false)
}

// Recover unconditionally republishes and reloads the committed projection;
// use it on startup/replay even when the on-disk bytes already match.
func (a *PoolApplier) Recover(ctx context.Context, name string, committed PoolState) error {
	b, err := renderState(name, committed)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.apply(ctx, name, committed.SiteID, b, b, true)
}

func poolRoot(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, rpcerr.Errf("invalid_argument", "invalid pool directory")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	defer unix.Close(fd)
	return os.OpenRoot(fmt.Sprintf("/proc/self/fd/%d", fd))
}
func poolRead(root *os.Root, name string) ([]byte, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, rpcerr.Errf("invalid_argument", "invalid pool file")
	}
	return io.ReadAll(f)
}
func poolSync(root *os.Root) error {
	f, e := root.Open(".")
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func poolWrite(root *os.Root, name string, b []byte) error {
	tmp := name + ".tmp"
	info, e := root.Lstat(tmp)
	if e == nil {
		if !info.Mode().IsRegular() {
			return rpcerr.Errf("invalid_argument", "invalid temporary pool file")
		}
		if e = root.Remove(tmp); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	f, e := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0644)
	if e != nil {
		return e
	}
	defer root.Remove(tmp)
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	if e = root.Rename(tmp, name); e != nil {
		return e
	}
	return poolSync(root)
}
func (a *PoolApplier) command(ctx context.Context, bin string, args ...string) error {
	_, _, code, err := a.Runner.Run(ctx, bin, args, runner.RunOpts{})
	if err != nil {
		return err
	}
	if code != 0 {
		return rpcerr.Errf("agent_error", "FPM command failed")
	}
	return nil
}

// testCandidate builds a transient master configuration listing the live pool
// set with this pool replaced by the candidate. The candidate is not included
// by the normal *.conf glob and cannot be served before the atomic rename.
func (a *PoolApplier) testCandidate(ctx context.Context, root *os.Root, file string, b []byte) error {
	if strings.ContainsAny(a.Dir, " \t\r\n;=\"'") {
		return rpcerr.Errf("invalid_argument", "invalid pool directory")
	}
	entries, err := os.ReadDir(a.Dir)
	if err != nil {
		return err
	}
	var paths []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".conf") || e.Name() == file {
			continue
		}
		if strings.ContainsAny(e.Name(), " \t\r\n;=\"'") {
			return rpcerr.Errf("invalid_argument", "invalid pool filename")
		}
		if _, err := poolRead(root, e.Name()); err != nil {
			return err
		}
		paths = append(paths, filepath.Join(a.Dir, e.Name()))
	}
	candidate := file + ".candidate"
	master := file + ".test"
	defer root.Remove(master)
	if b != nil {
		if err := poolWrite(root, candidate, b); err != nil {
			return err
		}
		paths = append(paths, filepath.Join(a.Dir, candidate))
	}
	sort.Strings(paths)
	// Preserve the installed master verbatim except for its pool glob. Includes are
	// expanded at their original position: FPM configuration is order-sensitive.
	main, err := os.ReadFile(filepath.Join(filepath.Dir(a.Dir), "php-fpm.conf"))
	if err != nil {
		return err
	}
	glob := filepath.Join(a.Dir, "*.conf")
	include := regexp.MustCompile(`(?i)^\s*include\s*=\s*([^\s;]+)\s*(?:;.*)?$`)
	lines := strings.Split(string(main), "\n")
	count := 0
	for i, line := range lines {
		m := include.FindStringSubmatch(line)
		if len(m) != 2 || m[1] != glob {
			continue
		}
		count++
		var replacement strings.Builder
		for _, path := range paths {
			fmt.Fprintf(&replacement, "include = %s\n", path)
		}
		lines[i] = strings.TrimSuffix(replacement.String(), "\n")
	}
	if count != 1 {
		return rpcerr.Errf("agent_error", "FPM master must include the pool directory exactly once")
	}
	if len(paths) == 0 {
		return nil
	}
	if err := poolWrite(root, master, []byte(strings.Join(lines, "\n"))); err != nil {
		return err
	}
	_, stderr, code, err := a.Runner.Run(ctx, "/usr/sbin/php-fpm8.3", []string{"-t", "-y", filepath.Join(a.Dir, master)}, runner.RunOpts{})
	if err != nil {
		return err
	}
	if code != 0 {
		return &fpmTestError{stderr: string(stderr)}
	}
	return nil
}

type fpmTestError struct{ stderr string }

func (*fpmTestError) Error() string { return "FPM command failed" }

// Attribute only a named, existing unmanaged file in an FPM diagnostic, never
// an execution error or an arbitrary error string.
func (a *PoolApplier) baselineError(root *os.Root, err error) error {
	var testErr *fpmTestError
	if !errors.As(err, &testErr) {
		return err
	}
	entries, e := os.ReadDir(a.Dir)
	if e != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".conf") || strings.HasPrefix(name, "novapanel-") {
			continue
		}
		path := filepath.Join(a.Dir, name)
		diagnostic := regexp.MustCompile(`(?im)^.*\[ERROR\]\s+(?:failed to (?:parse|open) configuration file|failed to process configuration file)\s+['"]` + regexp.QuoteMeta(path) + `['"](?:\s|:|$)`)
		if diagnostic.MatchString(testErr.stderr) {
			if _, e := poolRead(root, name); e == nil {
				return rpcerr.Errf("precondition_failed", "FPM baseline failed; fix unmanaged pool %s and retry", name)
			}
		}
	}
	return err
}

func poolPublish(root *os.Root, file string, b []byte) error {
	if b != nil {
		return poolWrite(root, file, b)
	}
	err := root.Remove(file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return poolSync(root)
}

func (a *PoolApplier) effectiveCount(root *os.Root, file string, present bool) (int, error) {
	entries, err := os.ReadDir(a.Dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".conf") && e.Name() != file {
			if _, err := poolRead(root, e.Name()); err != nil {
				return 0, err
			}
			n++
		}
	}
	if present {
		n++
	}
	return n, nil
}

func (a *PoolApplier) service(ctx context.Context, count int) error {
	if count == 0 {
		if err := a.command(ctx, "/usr/bin/systemctl", "stop", "php8.3-fpm.service"); err != nil {
			return err
		}
		return a.command(ctx, "/usr/bin/systemctl", "disable", "php8.3-fpm.service")
	}
	if err := a.command(ctx, "/usr/bin/systemctl", "enable", "--now", "php8.3-fpm.service"); err != nil {
		return err
	}
	if err := a.command(ctx, "/usr/bin/systemctl", "reload", "php8.3-fpm.service"); err != nil {
		return err
	}
	return a.Probe(ctx)
}

func (a *PoolApplier) apply(ctx context.Context, name, siteID string, b, committed []byte, recovery bool) error {
	if a.Runner == nil || a.Probe == nil {
		return rpcerr.Errf("agent_error", "FPM apply dependencies unavailable")
	}
	root, err := poolRoot(a.Dir)
	if err != nil {
		return err
	}
	defer root.Close()
	file := "novapanel-" + name + ".conf"
	// The header is the ownership proof, including during rollback and replay.
	current, err := poolRead(root, file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && !strings.HasPrefix(string(current), "; novapanel-managed site="+siteID+"\n") {
		return rpcerr.Errf("conflict", "pool file is not owned by site")
	}
	restore := func() error {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := poolPublish(root, file, committed); err != nil {
			return err
		}
		count, err := a.effectiveCount(root, file, committed != nil)
		if err != nil {
			return err
		}
		return a.service(c, count)
	}
	defer root.Remove(file + ".candidate")
	if !recovery {
		// Test the existing effective set before substituting the desired pool.
		if err = a.testCandidate(ctx, root, "", nil); err != nil {
			err = a.baselineError(root, err)
			if rerr := restore(); rerr != nil {
				return errors.Join(err, rerr, jobs.ErrRetry)
			}
			return err
		}
	}
	if err = a.testCandidate(ctx, root, file, b); err == nil {
		if b == nil {
			err = poolPublish(root, file, nil)
		} else {
			err = root.Rename(file+".candidate", file)
			if err == nil {
				err = poolSync(root)
			}
		}
	}
	if err == nil {
		var count int
		count, err = a.effectiveCount(root, file, b != nil)
		if err == nil {
			err = a.service(ctx, count)
		}
	}
	if err == nil {
		return nil
	}
	if recovery {
		return errors.Join(err, jobs.ErrRetry)
	}
	if rerr := restore(); rerr != nil {
		return errors.Join(err, rerr, jobs.ErrRetry)
	}
	return err
}
