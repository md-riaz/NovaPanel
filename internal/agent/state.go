package agent

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"novapanel/internal/audit"
	"novapanel/internal/exec"
	"novapanel/internal/platform"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"os"
	"path/filepath"
)

// OpenState initializes root-held state before the transport accepts requests.
// The runner is injected so tests never need immutable-file capabilities.
func (a *Agent) OpenState(ctx context.Context) (func() error, error) {
	for _, d := range []struct {
		p string
		m os.FileMode
	}{{filepath.Dir(a.Cfg.DBPath), 0700}, {filepath.Dir(a.Cfg.SealKeyPath), 0700}, {a.Cfg.LogDir, 0750}, {filepath.Dir(a.Cfg.SocketPath), 0755}, {a.Cfg.ACMEWebroot, 0755}} {
		// Reject writable or foreign-owned ancestors too, not only the final directory.
		for p := d.p; ; p = filepath.Dir(p) {
			var st unix.Stat_t
			if err := unix.Lstat(p, &st); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}
			if st.Mode&unix.S_IFMT != unix.S_IFDIR || (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) || st.Mode&0022 != 0 {
				// A sticky root-owned temporary ancestor cannot replace a child owned by root.
				if st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Uid != 0 || st.Mode&unix.S_ISVTX == 0 || p == d.p {
					return nil, fmt.Errorf("unsafe state directory %s", p)
				}
			}
			if p == filepath.Dir(p) {
				break
			}
		}
		if err := os.MkdirAll(d.p, d.m); err != nil {
			return nil, err
		}
		if d.m == 0700 {
			st, err := os.Stat(d.p)
			if err != nil {
				return nil, err
			}
			if st.Mode().Perm() != 0700 {
				return nil, fmt.Errorf("state directory must be private: %s", d.p)
			}
		}
	}
	s, err := store.Open(a.Cfg.DBPath)
	if err != nil {
		return nil, err
	}
	sealer, err := secret.LoadOrCreate(a.Cfg.SealKeyPath)
	if err != nil {
		s.Close()
		return nil, err
	}
	path := filepath.Join(a.Cfg.LogDir, "audit.log")
	log, err := audit.Open(s, path)
	if err != nil {
		s.Close()
		return nil, err
	}
	cleanup := func() error { return errors.Join(log.Close(), s.Close()) }
	_, _, code, err := a.Exec.Run(ctx, platform.Chattr, []string{"+a", path}, exec.RunOpts{})
	if err != nil || code != 0 {
		cleanup()
		return nil, fmt.Errorf("audit append-only enforcement failed: exit=%d: %v", code, err)
	}
	if err = log.Record(audit.Event{Actor: "agent", Role: "owner", Verb: "agent.start", Outcome: "ok"}); err != nil {
		cleanup()
		return nil, err
	}
	a.Store = s
	a.Secret = sealer
	a.Audit = log
	return cleanup, nil
}
