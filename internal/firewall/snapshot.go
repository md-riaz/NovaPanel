package firewall

import (
	"bytes"
	"context"
	"crypto/sha256"
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

const snapshotFileLimit = 4 << 20
const snapshotTotalLimit = 16 << 20
const snapshotManifestLimit = 4096

var errSnapshot = errors.New("firewall snapshot custody refused")

// SnapshotInput contains already captured bytes, never commands or source paths.
// Callers must not mutate input concurrently with Put.
type SnapshotInput struct {
	PriorEnabled                                bool
	Rules                                       map[string][]byte
	IPTablesSave, IP6TablesSave, NumberedStatus []byte
}
type snapshotArtifact struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type snapshotManifest struct {
	Version      int                `json:"version"`
	ID           string             `json:"id"`
	PriorEnabled bool               `json:"prior_enabled"`
	Artifacts    []snapshotArtifact `json:"artifacts"`
}

var snapshotNames = [...]string{"enabled", "ip6tables-save", "iptables-save", "ufw-status-numbered", "ufw/after.rules", "ufw/after6.rules", "ufw/before.rules", "ufw/before6.rules", "ufw/user.rules", "ufw/user6.rules"}

// SnapshotCustody holds an admitted root identity. Put serializes across handles
// and processes; trusted UID must not otherwise mutate custody paths or bytes.
// Close must not race other methods.
type SnapshotCustody struct {
	root     string
	dir      *os.File
	syncFile func(*os.File) error
	rename   func(int, string, int, string) error
}

// VerifiedSnapshot retains read-only descriptors at offset zero, not paths.
// Package-private artifacts prevent a future consumer from reopening a pathname.
type VerifiedSnapshot struct {
	manifest snapshotManifest
	files    map[string]*os.File
	dirs     []*os.File
}

func (s *VerifiedSnapshot) Close() error {
	var err error
	for _, f := range s.files {
		err = errors.Join(err, f.Close())
	}
	for _, d := range s.dirs {
		err = errors.Join(err, d.Close())
	}
	s.files = nil
	s.dirs = nil
	if err != nil {
		return errSnapshot
	}
	return nil
}
func (c *SnapshotCustody) Close() error {
	if c.dir.Close() != nil {
		return errSnapshot
	}
	return nil
}
func snapshotRoot(root string) (*os.File, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
		return nil, errSnapshot
	}
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, errSnapshot
	}
	for _, part := range strings.Split(strings.TrimPrefix(root, "/"), "/") {
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if err != nil {
			return nil, errSnapshot
		}
		fd = next
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) || st.Mode&0022 != 0 {
			unix.Close(fd)
			return nil, errSnapshot
		}
	}
	f := os.NewFile(uintptr(fd), "custody")
	if !snapshotDirectory(f) {
		f.Close()
		return nil, errSnapshot
	}
	return f, nil
}
func snapshotDirectory(f *os.File) bool {
	var st unix.Stat_t
	return unix.Fstat(int(f.Fd()), &st) == nil && st.Mode&unix.S_IFMT == unix.S_IFDIR && st.Mode&07777 == 0700 && st.Uid == uint32(os.Geteuid())
}
func OpenSnapshotCustody(root string) (*SnapshotCustody, error) {
	d, err := snapshotRoot(root)
	if err != nil {
		return nil, errSnapshot
	}
	return &SnapshotCustody{root: root, dir: d}, nil
}
func (c *SnapshotCustody) check(ctx context.Context) error {
	if ctx.Err() != nil {
		return errSnapshot
	}
	d, e := snapshotRoot(c.root)
	if e != nil {
		return errSnapshot
	}
	defer d.Close()
	a, e := c.dir.Stat()
	if e != nil {
		return errSnapshot
	}
	b, e := d.Stat()
	if e != nil || !os.SameFile(a, b) {
		return errSnapshot
	}
	return nil
}
func (c *SnapshotCustody) sync(f *os.File) error {
	if c.syncFile != nil {
		return c.syncFile(f)
	}
	return f.Sync()
}
func snapshotDigest(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func snapshotInput(pendingID string, in SnapshotInput) (snapshotManifest, [][]byte, []byte, error) {
	m := snapshotManifest{Version: 1, ID: pendingID, PriorEnabled: in.PriorEnabled}
	if !id.Valid(pendingID) || len(in.Rules) != 6 {
		return m, nil, nil, errSnapshot
	}
	data := make([][]byte, len(snapshotNames))
	total := 0
	for i, n := range snapshotNames {
		var b []byte
		switch n {
		case "enabled":
			if in.PriorEnabled {
				b = []byte("true\n")
			} else {
				b = []byte("false\n")
			}
		case "ip6tables-save":
			b = in.IP6TablesSave
		case "iptables-save":
			b = in.IPTablesSave
		case "ufw-status-numbered":
			b = in.NumberedStatus
		default:
			b = in.Rules[strings.TrimPrefix(n, "ufw/")]
		}
		data[i] = b
		if len(b) == 0 || len(b) > snapshotFileLimit || total > snapshotTotalLimit-len(b) {
			return m, nil, nil, errSnapshot
		}
		total += len(b)
	}
	// Bound the entire input before allocating copies.
	for i, n := range snapshotNames {
		data[i] = bytes.Clone(data[i])
		m.Artifacts = append(m.Artifacts, snapshotArtifact{n, int64(len(data[i])), snapshotDigest(data[i])})
	}
	encoded, _ := json.Marshal(m)
	if len(encoded) > snapshotManifestLimit {
		return m, nil, nil, errSnapshot
	}
	return m, data, encoded, nil
}
func snapshotOpenDir(parent *os.File, name string) (*os.File, error) {
	fd, e := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, errSnapshot
	}
	f := os.NewFile(uintptr(fd), name)
	if !snapshotDirectory(f) {
		f.Close()
		return nil, errSnapshot
	}
	return f, nil
}
func snapshotEntries(d *os.File, want []string) error {
	// Independent directory stream avoids sharing offsets across callers.
	fd, e := unix.Openat(int(d.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return errSnapshot
	}
	f := os.NewFile(uintptr(fd), "entries")
	defer f.Close()
	names, e := f.Readdirnames(len(want) + 1)
	if (e != nil && e != io.EOF) || len(names) != len(want) {
		return errSnapshot
	}
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	for _, n := range want {
		if !seen[n] {
			return errSnapshot
		}
	}
	return nil
}
func snapshotRead(f *os.File, limit int64) ([]byte, error) {
	st, e := f.Stat()
	if e != nil || st.Size() <= 0 || st.Size() > limit {
		return nil, errSnapshot
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(b)) != st.Size() {
		return nil, errSnapshot
	}
	return b, nil
}
func (c *SnapshotCustody) verify(ctx context.Context, r JournalRecord) (_ *VerifiedSnapshot, err error) {
	if !id.Valid(r.ID) || c.check(ctx) != nil {
		return nil, errSnapshot
	}
	var staging unix.Stat_t
	if unix.Fstatat(int(c.dir.Fd()), r.ID+".next", &staging, unix.AT_SYMLINK_NOFOLLOW) != unix.ENOENT {
		return nil, errSnapshot
	}
	s := &VerifiedSnapshot{files: map[string]*os.File{}}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()
	d, e := snapshotOpenDir(c.dir, r.ID)
	if e != nil {
		return nil, errSnapshot
	}
	s.dirs = append(s.dirs, d)
	if snapshotEntries(d, []string{"manifest.json", "enabled", "ip6tables-save", "iptables-save", "ufw-status-numbered", "ufw"}) != nil {
		return nil, errSnapshot
	}
	u, e := snapshotOpenDir(d, "ufw")
	if e != nil {
		return nil, errSnapshot
	}
	s.dirs = append(s.dirs, u)
	if snapshotEntries(u, []string{"after.rules", "after6.rules", "before.rules", "before6.rules", "user.rules", "user6.rules"}) != nil {
		return nil, errSnapshot
	}
	mf, e := journalFile(int(d.Fd()), "manifest.json", unix.O_RDONLY)
	if e != nil {
		return nil, errSnapshot
	}
	b, e := snapshotRead(mf, snapshotManifestLimit)
	mf.Close()
	if e != nil || snapshotDigest(b) != r.SnapshotSHA256 {
		return nil, errSnapshot
	}
	if json.Unmarshal(b, &s.manifest) != nil {
		return nil, errSnapshot
	}
	m := s.manifest
	canonical, _ := json.Marshal(m)
	if !bytes.Equal(canonical, b) || m.Version != 1 || m.ID != r.ID || m.PriorEnabled != r.PriorEnabled || len(m.Artifacts) != len(snapshotNames) {
		return nil, errSnapshot
	}
	total := int64(0)
	// Validate every declared size and total before reading artifact bytes.
	for i, a := range m.Artifacts {
		if a.Name != snapshotNames[i] || a.Size <= 0 || a.Size > snapshotFileLimit || total > snapshotTotalLimit-a.Size {
			return nil, errSnapshot
		}
		total += a.Size
	}
	for _, a := range m.Artifacts {
		if ctx.Err() != nil {
			return nil, errSnapshot
		}
		parent := d
		name := a.Name
		if strings.HasPrefix(name, "ufw/") {
			parent = u
			name = strings.TrimPrefix(name, "ufw/")
		}
		f, e := journalFile(int(parent.Fd()), name, unix.O_RDONLY)
		if e != nil {
			return nil, errSnapshot
		}
		s.files[a.Name] = f
		data, e := snapshotRead(f, a.Size)
		if e != nil || int64(len(data)) != a.Size || snapshotDigest(data) != a.SHA256 {
			return nil, errSnapshot
		}
		if a.Name == "enabled" {
			expected := "false\n"
			if r.PriorEnabled {
				expected = "true\n"
			}
			if string(data) != expected {
				return nil, errSnapshot
			}
		}
		if _, e = f.Seek(0, io.SeekStart); e != nil {
			return nil, errSnapshot
		}
	}
	if c.sync(u) != nil || c.sync(d) != nil || c.sync(c.dir) != nil || c.check(ctx) != nil {
		return nil, errSnapshot
	}
	return s, nil
}

// Verify binds complete canonical custody to the journal's ID, digest and enabled state.
func (c *SnapshotCustody) Verify(ctx context.Context, r JournalRecord) (*VerifiedSnapshot, error) {
	return c.verify(ctx, r)
}

// Put publishes once by ID. An error after rename may leave complete visible
// custody; identical retry revalidates and syncs, never replaces it.
func (c *SnapshotCustody) Put(ctx context.Context, pendingID string, in SnapshotInput) (string, error) {
	_, data, b, e := snapshotInput(pendingID, in)
	if e != nil || c.check(ctx) != nil {
		return "", errSnapshot
	}
	// Reopen the pinned directory for a distinct flock description on every call.
	// Locking c.dir itself would let calls sharing this handle bypass each other.
	lock, e := snapshotOpenDir(c.dir, ".")
	if e != nil {
		return "", errSnapshot
	}
	defer lock.Close()
	for {
		e = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if e == nil {
			break
		}
		if e != unix.EWOULDBLOCK && e != unix.EAGAIN {
			return "", errSnapshot
		}
		select {
		case <-ctx.Done():
			return "", errSnapshot
		case <-time.After(5 * time.Millisecond):
		}
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	if c.check(ctx) != nil {
		return "", errSnapshot
	}
	dir := int(c.dir.Fd())
	stage := pendingID + ".next"
	var st unix.Stat_t
	if e = unix.Fstatat(dir, stage, &st, unix.AT_SYMLINK_NOFOLLOW); e != unix.ENOENT {
		return "", errSnapshot
	}
	digest := snapshotDigest(b)
	r := JournalRecord{ID: pendingID, SnapshotSHA256: digest, PriorEnabled: in.PriorEnabled}
	if e = unix.Fstatat(dir, pendingID, &st, unix.AT_SYMLINK_NOFOLLOW); e == nil {
		s, err := c.verify(ctx, r)
		if err != nil {
			return "", errSnapshot
		}
		if s.Close() != nil {
			return "", errSnapshot
		}
		return digest, nil
	} else if e != unix.ENOENT {
		return "", errSnapshot
	}
	if unix.Mkdirat(dir, stage, 0700) != nil {
		return "", errSnapshot
	}
	d, e := snapshotOpenDir(c.dir, stage)
	if e != nil {
		return "", errSnapshot
	}
	defer d.Close()
	if unix.Mkdirat(int(d.Fd()), "ufw", 0700) != nil {
		return "", errSnapshot
	}
	u, e := snapshotOpenDir(d, "ufw")
	if e != nil {
		return "", errSnapshot
	}
	defer u.Close()
	write := func(parent *os.File, name string, data []byte) error {
		if ctx.Err() != nil {
			return errSnapshot
		}
		f, err := journalFile(int(parent.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
		if err != nil {
			return errSnapshot
		}
		n, err := f.Write(data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
		if err == nil {
			err = c.sync(f)
		}
		ce := f.Close()
		if err != nil || ce != nil {
			return errSnapshot
		}
		return nil
	}
	for i, n := range snapshotNames {
		parent := d
		name := n
		if strings.HasPrefix(n, "ufw/") {
			parent = u
			name = strings.TrimPrefix(n, "ufw/")
		}
		if write(parent, name, data[i]) != nil {
			return "", errSnapshot
		}
	}
	if write(d, "manifest.json", b) != nil || c.sync(u) != nil || c.sync(d) != nil || c.check(ctx) != nil {
		return "", errSnapshot
	}
	if ctx.Err() != nil {
		return "", errSnapshot
	}
	if c.rename != nil {
		e = c.rename(dir, stage, dir, pendingID)
	} else {
		e = unix.Renameat2(dir, stage, dir, pendingID, unix.RENAME_NOREPLACE)
	}
	if e != nil || c.sync(c.dir) != nil || c.check(ctx) != nil {
		return "", errSnapshot
	}
	return digest, nil
}
