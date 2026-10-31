package firewall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	execpkg "novapanel/internal/exec"
	"novapanel/internal/id"
)

var errRestore = errors.New("confined firewall restoration refused")

// ConfinedRestore installs only the closed six-rule inventory below a trusted,
// preprovisioned root. Commands still act through the supplied Runner, which must
// be a fake in confined tests. This adapter is not wired into any runtime path.
// Close must not race Restore. Trusted UID must not mutate the admitted paths.
type ConfinedRestore struct {
	root                string
	dir, rules, markers *os.File
	runner              execpkg.Runner
}

type restoreMarker struct {
	Version        int    `json:"version"`
	ID             string `json:"id"`
	SnapshotSHA256 string `json:"snapshot_sha256"`
	PriorEnabled   bool   `json:"prior_enabled"`
}

func OpenConfinedRestore(root string, runner execpkg.Runner) (*ConfinedRestore, error) {
	if runner == nil {
		return nil, errRestore
	}
	d, e := snapshotRoot(root)
	if e != nil {
		return nil, errRestore
	}
	a := &ConfinedRestore{root: root, dir: d, runner: runner}
	a.rules, e = snapshotOpenDir(d, "ufw")
	if e != nil {
		d.Close()
		return nil, errRestore
	}
	a.markers, e = snapshotOpenDir(d, "markers")
	if e != nil {
		a.rules.Close()
		d.Close()
		return nil, errRestore
	}
	return a, nil
}
func (a *ConfinedRestore) Close() error {
	return errors.Join(a.dir.Close(), a.rules.Close(), a.markers.Close())
}
func (a *ConfinedRestore) check(ctx context.Context) error {
	if ctx.Err() != nil {
		return errRestore
	}
	d, e := snapshotRoot(a.root)
	if e != nil {
		return errRestore
	}
	defer d.Close()
	for _, p := range []struct {
		held *os.File
		name string
	}{{a.dir, "."}, {a.rules, "ufw"}, {a.markers, "markers"}} {
		current, e := snapshotOpenDir(d, p.name)
		if e != nil {
			return errRestore
		}
		old, x := p.held.Stat()
		now, y := current.Stat()
		current.Close()
		if x != nil || y != nil || !os.SameFile(old, now) {
			return errRestore
		}
	}
	return nil
}

// restoreRead uses retained descriptors without changing their borrowed offsets.
func restoreRead(f *os.File, size int64) ([]byte, error) {
	if f == nil || size <= 0 || size > snapshotFileLimit {
		return nil, errRestore
	}
	st, e := f.Stat()
	if e != nil || st.Size() != size {
		return nil, errRestore
	}
	b, e := io.ReadAll(io.NewSectionReader(f, 0, size+1))
	if e != nil || int64(len(b)) != size {
		return nil, errRestore
	}
	return b, nil
}
func restoreData(r JournalRecord, s *VerifiedSnapshot) ([][]byte, []byte, error) {
	if s == nil || !id.Valid(r.ID) {
		return nil, nil, errRestore
	}
	m := s.manifest
	encoded, _ := json.Marshal(m)
	if m.Version != 1 || m.ID != r.ID || m.PriorEnabled != r.PriorEnabled || snapshotDigest(encoded) != r.SnapshotSHA256 || len(m.Artifacts) != len(snapshotNames) {
		return nil, nil, errRestore
	}
	data := make([][]byte, 0, 6)
	for i, v := range m.Artifacts {
		if v.Name != snapshotNames[i] {
			return nil, nil, errRestore
		}
		if strings.HasPrefix(v.Name, "ufw/") {
			b, e := restoreRead(s.files[v.Name], v.Size)
			if e != nil || snapshotDigest(b) != v.SHA256 {
				return nil, nil, errRestore
			}
			data = append(data, b)
		}
	}
	marker, _ := json.Marshal(restoreMarker{1, r.ID, r.SnapshotSHA256, r.PriorEnabled})
	return data, marker, nil
}
func restoreExisting(d *os.File, name string) error {
	f, e := journalFile(int(d.Fd()), name, unix.O_RDONLY)
	if errors.Is(e, unix.ENOENT) {
		return nil
	}
	if e != nil {
		return errRestore
	}
	return f.Close()
}
func (a *ConfinedRestore) publish(ctx context.Context, d *os.File, name string, b []byte) error {
	if a.check(ctx) != nil || restoreExisting(d, name) != nil {
		return errRestore
	}
	stage := name + ".next"
	// Interrupted mutable restoration staging may be removed only after admission.
	if restoreExisting(d, stage) != nil {
		return errRestore
	}
	if e := unix.Unlinkat(int(d.Fd()), stage, 0); e != nil && e != unix.ENOENT {
		return errRestore
	}
	f, e := journalFile(int(d.Fd()), stage, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if e != nil {
		return errRestore
	}
	n, e := f.Write(b)
	if e == nil && n != len(b) {
		e = io.ErrShortWrite
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil || ce != nil || a.check(ctx) != nil || restoreExisting(d, name) != nil {
		return errRestore
	}
	if unix.Renameat(int(d.Fd()), stage, int(d.Fd()), name) != nil || d.Sync() != nil || a.check(ctx) != nil {
		return errRestore
	}
	return nil
}

// Restore must be called through Journal.FireVerified/RevertVerified. Success
// means rules, enabled-state commands and bound marker have completed durably.
// Any error requires whole-operation same-ID replay, including matching markers.
func (a *ConfinedRestore) Restore(ctx context.Context, r JournalRecord, s *VerifiedSnapshot) error {
	data, marker, e := restoreData(r, s)
	if e != nil || a.check(ctx) != nil {
		return errRestore
	}
	lock, e := snapshotOpenDir(a.dir, ".")
	if e != nil {
		return errRestore
	}
	defer lock.Close()
	for {
		e = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if e == nil {
			break
		}
		if e != unix.EWOULDBLOCK && e != unix.EAGAIN {
			return errRestore
		}
		select {
		case <-ctx.Done():
			return errRestore
		case <-time.After(5 * time.Millisecond):
		}
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	if a.check(ctx) != nil {
		return errRestore
	}
	name := r.ID + ".json"
	if restoreExisting(a.markers, name+".next") != nil {
		return errRestore
	}
	f, e := journalFile(int(a.markers.Fd()), name, unix.O_RDONLY)
	if e == nil {
		b, re := snapshotRead(f, journalLimit)
		ce := f.Close()
		if re != nil || ce != nil || !bytes.Equal(b, marker) {
			return errRestore
		}
	} else if !errors.Is(e, unix.ENOENT) {
		return errRestore
	}
	// Admit all leaves before changing any rules.
	for _, n := range snapshotNames[4:] {
		if restoreExisting(a.rules, strings.TrimPrefix(n, "ufw/")) != nil || restoreExisting(a.rules, strings.TrimPrefix(n, "ufw/")+".next") != nil {
			return errRestore
		}
	}
	for i, n := range snapshotNames[4:] {
		if a.publish(ctx, a.rules, strings.TrimPrefix(n, "ufw/"), data[i]) != nil {
			return errRestore
		}
	}
	plan := [][]string{{"disable"}}
	if r.PriorEnabled {
		plan = [][]string{{"--force", "enable"}, {"reload"}}
	}
	for _, args := range plan {
		if a.check(ctx) != nil {
			return errRestore
		}
		_, _, exit, e := a.runner.Run(ctx, "/usr/sbin/ufw", args, execpkg.RunOpts{})
		if e != nil || exit != 0 || a.check(ctx) != nil {
			return errRestore
		}
	}
	return a.publish(ctx, a.markers, name, marker)
}
