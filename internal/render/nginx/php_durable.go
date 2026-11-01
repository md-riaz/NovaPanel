package nginx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
	"novapanel/internal/platform"
	"novapanel/internal/render/php"
)

// PHPDurableBinding is supplied exclusively from the trusted persisted job and
// site lock, never from request paths. Root is a pre-created root-owned 0700
// directory dedicated to publication recovery. The caller must keep the job
// blocked until recovery and terminal metadata have both been reconciled.
type PHPDurableBinding struct {
	Root, JobID, SiteID string
	// Check must re-read the trusted journal's job binding, expiry, and recovery
	// interlock. This prototype is not wired to a production journal.
	Check func(context.Context) error
}

type phpOriginal struct {
	Version   int          `json:"version"`
	JobID     string       `json:"job_id"`
	SiteID    string       `json:"site_id"`
	NginxRoot string       `json:"nginx_root"`
	PHPRoot   string       `json:"php_root"`
	Nginx     Snapshot     `json:"nginx"`
	PHP       php.Snapshot `json:"php"`
}
type phpEnvelope struct {
	Payload json.RawMessage `json:"payload"`
	SHA256  string          `json:"sha256"`
}

// ErrPHPRecoveryRequired means saved originals have not been proved live.
var ErrPHPRecoveryRequired = errors.New("PHP snapshot recovery required")

func openPHPJournal(b PHPDurableBinding) (*os.Root, string, error) {
	if b.JobID == "" || b.SiteID == "" || len(b.JobID) > 128 || len(b.SiteID) > 128 {
		return nil, "", fmt.Errorf("invalid durable binding")
	}
	for _, s := range []string{b.JobID, b.SiteID} {
		for _, c := range s {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return nil, "", fmt.Errorf("invalid durable binding")
			}
		}
	}
	resolved, err := filepath.EvalSymlinks(b.Root)
	if err != nil || !filepath.IsAbs(b.Root) || resolved != b.Root {
		return nil, "", fmt.Errorf("invalid recovery directory")
	}
	r, err := os.OpenRoot(b.Root)
	if err != nil {
		return nil, "", err
	}
	f, err := r.Open(".")
	if err != nil {
		r.Close()
		return nil, "", err
	}
	var st unix.Stat_t
	err = unix.Fstat(int(f.Fd()), &st)
	f.Close()
	if err != nil || st.Uid != 0 || st.Mode&0777 != 0700 {
		r.Close()
		return nil, "", fmt.Errorf("invalid recovery custody")
	}
	return r, b.JobID + ".json", nil
}
func readPHPOriginal(r *os.Root, name string) (phpOriginal, error) {
	var s phpOriginal
	f, err := r.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return s, err
	}
	defer f.Close()
	var st unix.Stat_t
	if err = unix.Fstat(int(f.Fd()), &st); err != nil {
		return s, err
	}
	if st.Uid != 0 || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0777 != 0400 || st.Nlink != 1 || st.Size > 1024*1024 {
		return s, fmt.Errorf("invalid recovery snapshot custody")
	}
	data, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	if err != nil {
		return s, err
	}
	var e phpEnvelope
	if err = json.Unmarshal(data, &e); err != nil {
		return s, err
	}
	h := sha256.Sum256(e.Payload)
	if hex.EncodeToString(h[:]) != e.SHA256 {
		return s, fmt.Errorf("invalid recovery checksum")
	}
	err = json.Unmarshal(e.Payload, &s)
	return s, err
}
func writePHPOriginal(r *os.Root, name string, s phpOriginal) error {
	payload, err := json.Marshal(s)
	if err != nil {
		return err
	}
	h := sha256.Sum256(payload)
	data, err := json.Marshal(phpEnvelope{Payload: payload, SHA256: hex.EncodeToString(h[:])})
	if err != nil {
		return err
	}
	if len(data) > 1024*1024 {
		return fmt.Errorf("recovery snapshot too large")
	}
	// Link creates the final immutable name exclusively: never replace an existing
	// job's snapshot. The directory sync makes that name durable before staging.
	tmp := name + ".tmp"
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0400)
	if err != nil {
		return err
	}
	defer r.Remove(tmp)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = r.Link(tmp, name); err != nil {
		return err
	}
	if err = r.Remove(tmp); err != nil {
		return err
	}
	return syncDirectory(r)
}

// ApplyPHPDurable publishes under both renderer locks. On seeing a previously
// persisted snapshot it restores and validates the original, then returns an
// error: the journal owner must reconcile the job before admitting any retry.
// The snapshot is deliberately retained even after success; its lifecycle is
// owned by the caller's durable terminal reconciliation, never this method.
func (a *Applier) ApplyPHPDurable(ctx context.Context, pools *php.Applier, d Desired, binding PHPDurableBinding, commit func(context.Context) error, restoredProbe func(context.Context) error) error {
	if pools == nil || d.PHP == nil || commit == nil || restoredProbe == nil || binding.Check == nil || a.Runner == nil || a.Probe == nil || pools.Probe == nil {
		return fmt.Errorf("PHP transaction dependencies unavailable")
	}
	if d.Name != d.PHP.Name {
		return fmt.Errorf("site identity mismatch")
	}
	rendered, err := Render(d)
	if err != nil {
		return err
	}
	r, name, err := openPHPJournal(binding)
	if err != nil {
		return err
	}
	defer r.Close()
	// Reject replaced directory paths before each action; external commands use
	// absolute paths, unlike the descriptor-relative snapshot operations.
	paths := []string{binding.Root, a.Root, filepath.Join(a.Root, "sites-available"), filepath.Join(a.Root, "sites-enabled"), pools.Root}
	identities := make([]os.FileInfo, len(paths))
	for i, path := range paths {
		info, e := os.Lstat(path)
		if e != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("invalid publication directory custody")
		}
		var st unix.Stat_t
		e = unix.Lstat(path, &st)
		if e != nil || st.Uid != uint32(os.Geteuid()) {
			return fmt.Errorf("invalid publication directory owner")
		}
		identities[i] = info
	}
	check := binding.Check
	binding.Check = func(ctx context.Context) error {
		if err := check(ctx); err != nil {
			return err
		}
		for i, path := range paths {
			now, e := os.Lstat(path)
			if e != nil || !now.IsDir() || !os.SameFile(identities[i], now) || now.Mode().Perm()&0022 != 0 {
				return fmt.Errorf("publication directory replaced")
			}
		}
		return nil
	}
	return a.Transaction(ctx, func(n *Transaction) error {
		if a.Guard != nil {
			if err := a.Guard(ctx); err != nil {
				return err
			}
		}
		return pools.Transaction(ctx, *d.PHP, func(p *php.Transaction) (result error) {
			original, err := readPHPOriginal(r, name)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			if err == nil {
				if original.Version != 1 || original.JobID != binding.JobID || original.SiteID != binding.SiteID || original.NginxRoot != a.Root || original.PHPRoot != pools.Root || original.Nginx.Name != d.Name {
					return fmt.Errorf("recovery binding mismatch")
				}
				if err = restorePHPOriginal(ctx, a, n, p, original, binding.Check, restoredProbe); err != nil {
					return errors.Join(ErrPHPRecoveryRequired, err)
				}
				return fmt.Errorf("PHP original restored; journal reconciliation required")
			}
			if err = binding.Check(ctx); err != nil {
				return err
			}
			old, err := n.Snapshot(d.Name)
			if err != nil {
				return err
			}
			original = phpOriginal{Version: 1, JobID: binding.JobID, SiteID: binding.SiteID, NginxRoot: a.Root, PHPRoot: pools.Root, Nginx: old, PHP: p.Snapshot()}
			if err = binding.Check(ctx); err != nil {
				return err
			}
			if err = writePHPOriginal(r, name, original); err != nil {
				return err
			}
			defer func() {
				if result != nil {
					recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
					defer cancel()
					if err := restorePHPOriginal(recovery, a, n, p, original, binding.Check, restoredProbe); err != nil {
						result = errors.Join(result, ErrPHPRecoveryRequired, err)
					}
				}
			}()
			if err = ctx.Err(); err != nil {
				return err
			}
			if err = binding.Check(ctx); err != nil {
				return err
			}
			if err = p.Stage(); err != nil {
				return err
			}
			staged := Snapshot{Name: d.Name, Config: rendered, LKG: old.LKG, Enabled: true}
			if err = binding.Check(ctx); err != nil {
				return err
			}
			if err = n.Restore(ctx, staged, false); err != nil {
				return err
			}
			if err = binding.Check(ctx); err != nil {
				return err
			}
			if err = p.Validate(ctx); err != nil {
				return err
			}
			if err = binding.Check(ctx); err != nil {
				return err
			}
			if err = a.command(ctx, platform.Nginx, []string{"-t"}); err != nil {
				return err
			}
			if err = binding.Check(ctx); err != nil {
				return err
			}
			if err = p.Reload(ctx); err != nil {
				return err
			}
			if err = binding.Check(ctx); err != nil {
				return err
			}
			if err = pools.Probe(ctx, *d.PHP); err != nil {
				return err
			}
			if err = binding.Check(ctx); err != nil {
				return err
			}
			if err = a.command(ctx, platform.Systemctl, []string{"reload", "nginx"}); err != nil {
				return err
			}
			if err = binding.Check(ctx); err != nil {
				return err
			}
			if err = a.Probe(ctx, d); err != nil {
				return err
			}
			if err = binding.Check(ctx); err != nil {
				return err
			}
			if err = p.Promote(); err != nil {
				return err
			}
			staged.LKG = rendered
			if err = binding.Check(ctx); err != nil {
				return err
			}
			if err = n.Restore(ctx, staged, false); err != nil {
				return err
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			if err = binding.Check(ctx); err != nil {
				return err
			}
			return commit(ctx)
		})
	})
}
func restorePHPOriginal(ctx context.Context, a *Applier, n *Transaction, p *php.Transaction, s phpOriginal, check func(context.Context) error, probe func(context.Context) error) error {
	if err := check(ctx); err != nil {
		return err
	}
	if err := errors.Join(p.RestoreSnapshot(s.PHP), n.Restore(ctx, s.Nginx, false)); err != nil {
		return err
	}
	if err := check(ctx); err != nil {
		return err
	}
	if err := p.Validate(ctx); err != nil {
		return err
	}
	if err := check(ctx); err != nil {
		return err
	}
	if err := a.command(ctx, platform.Nginx, []string{"-t"}); err != nil {
		return err
	}
	if err := check(ctx); err != nil {
		return err
	}
	if err := p.Reload(ctx); err != nil {
		return err
	}
	if err := check(ctx); err != nil {
		return err
	}
	if err := a.command(ctx, platform.Systemctl, []string{"reload", "nginx"}); err != nil {
		return err
	}
	if s.Nginx.Enabled {
		if err := check(ctx); err != nil {
			return err
		}
		return probe(ctx)
	}
	return nil
}
