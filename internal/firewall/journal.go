package firewall

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	"novapanel/internal/id"
)

const journalLimit = 2048

// JournalRecord contains metadata only. The trusted adapter must verify the
// ID-bound snapshot digest before restoring rules AND PriorEnabled, and publish
// an idempotent durable audit marker before returning. No arbitrary paths enter it.
type JournalRecord struct {
	Version        int    `json:"version"`
	ID             string `json:"id"`
	Unit           string `json:"unit"`
	Deadline       int64  `json:"deadline"`
	SnapshotSHA256 string `json:"snapshot_sha256"`
	PriorEnabled   bool   `json:"prior_enabled"`
	State          string `json:"state"`
}

// Journal requires a preprovisioned 0700 root and stable empty 0600 lock file.
// Trusted root/agent UID must not rename or mutate its paths concurrently.
// Each operation opens its own lock descriptor, also serializing same-process calls.
type Journal struct {
	root         string
	dir          *os.File
	lockIdentity os.FileInfo
}

func OpenJournal(root string) (*Journal, error) {
	d, err := journalRoot(root)
	if err != nil {
		return nil, err
	}
	j := &Journal{root: root, dir: d}
	lock, e := journalFile(int(d.Fd()), "lock", unix.O_RDONLY)
	if e != nil {
		d.Close()
		return nil, e
	}
	j.lockIdentity, e = lock.Stat()
	lock.Close()
	if e != nil {
		d.Close()
		return nil, e
	}
	err = j.locked(context.Background(), func() error { return nil })
	if err != nil {
		d.Close()
		return nil, err
	}
	return j, nil
}
func (j *Journal) Close() error { return j.dir.Close() }

func journalRoot(root string) (*os.File, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
		return nil, errors.New("unsafe journal root")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(root, "/"), "/") {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
		var st unix.Stat_t
		if e = unix.Fstat(fd, &st); e != nil {
			unix.Close(fd)
			return nil, e
		}
		// Root-owned sticky ancestors are safe for individually validated descendants.
		sticky := st.Uid == 0 && st.Mode&unix.S_ISVTX != 0
		if (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) || (st.Mode&0022 != 0 && !sticky) {
			unix.Close(fd)
			return nil, errors.New("unsafe journal ancestor")
		}
	}
	var st unix.Stat_t
	err = unix.Fstat(fd, &st)
	if err != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&07777 != 0700 {
		unix.Close(fd)
		return nil, errors.New("unsafe journal directory")
	}
	return os.NewFile(uintptr(fd), root), nil
}

func journalFile(dir int, name string, flags int) (*os.File, error) {
	fd, err := unix.Openat(dir, name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	var s unix.Stat_t
	if err = unix.Fstat(fd, &s); err != nil || s.Mode&unix.S_IFMT != unix.S_IFREG || s.Mode&07777 != 0600 || s.Uid != uint32(os.Geteuid()) || s.Nlink != 1 {
		unix.Close(fd)
		return nil, errors.New("unsafe journal file")
	}
	return os.NewFile(uintptr(fd), name), nil
}
func (j *Journal) locked(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d, err := journalRoot(j.root)
	if err != nil {
		return err
	}
	defer d.Close()
	a, e := j.dir.Stat()
	if e != nil {
		return e
	}
	b, e := d.Stat()
	if e != nil {
		return e
	}
	if !os.SameFile(a, b) {
		return errors.New("journal root changed")
	}
	f, err := journalFile(int(j.dir.Fd()), "lock", unix.O_RDWR)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(st, j.lockIdentity) {
		return errors.New("journal lock replaced")
	}
	if st.Size() != 0 {
		return errors.New("nonempty journal lock")
	}
	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond * 5):
		}
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)
	// Never unlink or replace lock. Detect replacement before making a decision.
	current, err := journalFile(int(j.dir.Fd()), "lock", unix.O_RDONLY)
	if err != nil {
		return err
	}
	cs, err := current.Stat()
	current.Close()
	if err != nil {
		return err
	}
	if !os.SameFile(st, cs) {
		return errors.New("journal lock changed")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return fn()
}
func validJournal(r JournalRecord) bool {
	digest, err := hex.DecodeString(r.SnapshotSHA256)
	if r.Version != 1 || !id.Valid(r.ID) || r.Unit != "novapanel-fwrevert-"+r.ID || r.Deadline <= 0 || r.Deadline > 253402300799 || err != nil || len(digest) != 32 || strings.ToLower(r.SnapshotSHA256) != r.SnapshotSHA256 {
		return false
	}
	switch r.State {
	case "armed", "confirmed", "reverting", "reverted", "acknowledged":
		return true
	}
	return false
}
func (j *Journal) read() (JournalRecord, error) {
	var r JournalRecord
	f, err := journalFile(int(j.dir.Fd()), "state", unix.O_RDONLY)
	if err != nil {
		return r, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, journalLimit+1))
	if err != nil {
		return r, err
	}
	if len(b) > journalLimit {
		return r, errors.New("journal too large")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&r); err != nil {
		return r, errors.New("invalid journal")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF || !validJournal(r) {
		return r, errors.New("invalid journal")
	}
	// Canonical encoding also rejects duplicate keys, missing fields and whitespace.
	canonical, _ := json.Marshal(r)
	if !bytes.Equal(b, canonical) {
		return r, errors.New("noncanonical journal")
	}
	return r, nil
}
func (j *Journal) publish(r JournalRecord) error {
	if !validJournal(r) {
		return errors.New("invalid journal record")
	}
	dir := int(j.dir.Fd())
	// A single bounded stale temp may remain after process death. Validate custody
	// before unlinking; never follow, repair or overwrite a hostile object.
	old, err := journalFile(dir, "state.next", unix.O_RDONLY)
	if err == nil {
		old.Close()
		if err = unix.Unlinkat(dir, "state.next", 0); err != nil {
			return err
		}
	} else if !errors.Is(err, unix.ENOENT) {
		return err
	}
	f, err := journalFile(dir, "state.next", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(r)
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = unix.Renameat(dir, "state.next", dir, "state"); err != nil {
		return err
	}
	return j.dir.Sync()
}
func (j *Journal) Arm(ctx context.Context, r JournalRecord) error {
	return j.locked(ctx, func() error {
		if _, err := j.read(); !errors.Is(err, unix.ENOENT) {
			if err != nil {
				return err
			}
			return errors.New("journal already occupied")
		}
		if r.State != "armed" {
			return errors.New("initial state must be armed")
		}
		return j.publish(r)
	})
}
func (j *Journal) Confirm(ctx context.Context, pendingID string, now func() time.Time, stop func(string) error) error {
	if stop == nil || now == nil {
		return errors.New("missing timer adapter")
	}
	return j.locked(ctx, func() error {
		r, err := j.read()
		if err != nil {
			return err
		}
		if r.ID != pendingID || !now().Before(time.Unix(r.Deadline, 0)) || (r.State != "armed" && r.State != "confirmed") {
			return notFound()
		}
		r.State = "confirmed"
		if err = j.publish(r); err != nil {
			return err
		}
		return stop(r.Unit)
	})
}

// Reconcile is daemon-only. commit must idempotently commit the exact ID's
// pending-row projection and audit event before returning. No helper calls it.
func (j *Journal) Reconcile(ctx context.Context, commit func(JournalRecord) error) error {
	if commit == nil {
		return errors.New("missing reconciliation adapter")
	}
	return j.locked(ctx, func() error {
		r, err := j.read()
		if err != nil {
			return err
		}
		if r.State == "acknowledged" {
			return nil
		}
		if r.State != "confirmed" && r.State != "reverted" {
			return errors.New("journal not terminal")
		}
		if err = commit(r); err != nil {
			return err
		}
		r.State = "acknowledged"
		return j.publish(r)
	})
}

func (j *Journal) Fire(ctx context.Context, pendingID string, now func() time.Time, restore func(JournalRecord) error) error {
	if restore == nil || now == nil {
		return errors.New("missing recovery adapter")
	}
	return j.locked(ctx, func() error {
		r, err := j.read()
		if err != nil {
			return err
		}
		if r.ID != pendingID {
			return notFound()
		}
		if r.State == "confirmed" || r.State == "reverted" || r.State == "acknowledged" {
			return nil
		}
		if now().Before(time.Unix(r.Deadline, 0)) {
			return fmt.Errorf("firewall deadline not reached")
		}
		r.State = "reverting"
		if err = j.publish(r); err != nil {
			return err
		}
		if err = restore(r); err != nil {
			return err
		}
		r.State = "reverted"
		return j.publish(r)
	})
}
