package agent

import (
	"context"
	"fmt"
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
func (a *Agent) OpenState(ctx context.Context) (func(), error) {
	for _, d := range []struct {
		p string
		m os.FileMode
	}{{filepath.Dir(a.Cfg.DBPath), 0700}, {filepath.Dir(a.Cfg.SealKeyPath), 0700}, {a.Cfg.LogDir, 0750}, {filepath.Dir(a.Cfg.SocketPath), 0755}, {a.Cfg.ACMEWebroot, 0755}} {
		if err := os.MkdirAll(d.p, d.m); err != nil {
			return nil, err
		}
		st, err := os.Lstat(d.p)
		if err != nil {
			return nil, err
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("unsafe state directory %s", d.p)
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
	cleanup := func() { log.Close(); s.Close() }
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
