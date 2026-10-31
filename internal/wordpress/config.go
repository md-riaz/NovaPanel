package wordpress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
	"novapanel/internal/id"
	"novapanel/internal/secret"
)

// ConfigBinding comes only from the sealed operation and resolved site account.
// The caller must hold the operation locks and isolate the site UID from writes.
// No binding field authorizes execution of PHP or accepts client-supplied paths.
type ConfigBinding struct {
	JobID, SiteID, DatabaseID, AccountID string
	Docroot                              string
	UID, GID                             uint32
}

type configRecord struct {
	Version int
	Binding ConfigBinding
	Config  []byte
}

var errConfigCustody = errors.New("wordpress: protected config custody refused")

// configOps substitutes failure boundaries, never path or ownership decisions.
type configOps struct {
	boundary         func(string) error
	fixtureAncestors bool // private scratch fixtures only; exported API cannot set it
}

func (o configOps) check(ctx context.Context, stage string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if o.boundary != nil {
		return o.boundary(stage)
	}
	return nil
}

// PublishConfig publishes execution-only bytes and an encrypted recovery record.
// custody must already be root-owned 0700 on the docroot filesystem. Retain the
// record until the operation owner reconciles terminality; this is not completion.
func PublishConfig(ctx context.Context, custody string, b ConfigBinding, config []byte, seal *secret.Sealer) error {
	if b.UID == 0 || b.GID == 0 {
		return errConfigCustody
	}
	return publishConfig(ctx, custody, b, config, seal, 0, configOps{})
}

func publishConfig(ctx context.Context, custody string, b ConfigBinding, config []byte, seal *secret.Sealer, owner uint32, ops configOps) (err error) {
	defer func() {
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			err = errConfigCustody
		}
	}()
	if seal == nil || len(config) == 0 || len(config) > 1<<20 || !id.Valid(b.JobID) || !id.Valid(b.SiteID) || !id.Valid(b.DatabaseID) || !id.Valid(b.AccountID) {
		return errConfigCustody
	}
	if err = ops.check(ctx, "open"); err != nil {
		return err
	}
	journal, err := openConfigDir(custody, owner, nil, ops.fixtureAncestors)
	if err != nil {
		return err
	}
	defer journal.Close()
	var js unix.Stat_t
	if unix.Fstat(int(journal.Fd()), &js) != nil || js.Mode&07777 != 0700 {
		return errConfigCustody
	}
	target, err := openConfigDir(b.Docroot, owner, &b, ops.fixtureAncestors)
	if err != nil {
		return err
	}
	defer target.Close()
	var ds unix.Stat_t
	if unix.Fstat(int(target.Fd()), &ds) != nil || js.Dev != ds.Dev {
		return errConfigCustody
	}
	recordName := b.JobID + ".sealed"
	expected := configRecord{Version: 1, Binding: b, Config: config}
	// Only an existing authenticated record can justify an existing destination.
	raw, found, err := readConfigFile(journal, recordName, owner, uint32(os.Getegid()), 0600, false)
	if err != nil {
		return err
	}
	if found {
		var envelope secret.Sealed
		if json.Unmarshal(raw, &envelope) != nil {
			return errConfigCustody
		}
		decoded, e := seal.Open(envelope)
		if e != nil {
			return errConfigCustody
		}
		defer clear(decoded)
		var record configRecord
		if json.Unmarshal(decoded, &record) != nil {
			return errConfigCustody
		}
		defer clear(record.Config)
		if record.Version != 1 || record.Binding != b || !bytes.Equal(record.Config, config) {
			return errConfigCustody
		}
	} else {
		_, exists, e := readConfigFile(target, "wp-config.php", b.UID, b.GID, 0400, true)
		if e != nil || exists {
			return errConfigCustody
		}
		// No durable record means publication has not started. Discard only a
		// safe agent-owned encrypted preparation file, never an unsafe entry.
		_, stale, e := readConfigFile(journal, recordName+".tmp", owner, uint32(os.Getegid()), 0600, true)
		if e != nil {
			return e
		}
		if stale {
			if e = unix.Unlinkat(int(journal.Fd()), recordName+".tmp", 0); e != nil {
				return e
			}
		}
		encoded, e := json.Marshal(expected)
		if e != nil {
			return e
		}
		envelope, e := seal.Seal(encoded)
		clear(encoded)
		if e != nil {
			return e
		}
		encoded, e = json.Marshal(envelope)
		if e != nil {
			return e
		}
		if err = writeConfigFile(ctx, journal, recordName+".tmp", encoded, owner, uint32(os.Getegid()), 0600, ops, "record"); err != nil {
			return err
		}
		if err = ops.check(ctx, "record-rename"); err != nil {
			return err
		}
		if err = unix.Renameat2(int(journal.Fd()), recordName+".tmp", int(journal.Fd()), recordName, unix.RENAME_NOREPLACE); err != nil {
			return err
		}
	}
	// A record rename alone is not permission to publish: sync it on every retry.
	if err = ops.check(ctx, "record-dir-sync"); err != nil {
		return err
	}
	if err = journal.Sync(); err != nil {
		return err
	}
	current, exists, err := readConfigFile(target, "wp-config.php", b.UID, b.GID, 0400, true)
	if err != nil {
		return err
	}
	defer clear(current)
	if exists {
		if !bytes.Equal(current, config) {
			return errConfigCustody
		}
	} else {
		temp := b.JobID + ".config"
		// A process may die before chmod, chown or write completes. Only the
		// authenticated record permits discarding its private temporary inode.
		var st unix.Stat_t
		e := unix.Fstatat(int(journal.Fd()), temp, &st, unix.AT_SYMLINK_NOFOLLOW)
		if e == nil {
			if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || (st.Uid != owner && st.Uid != b.UID) || (st.Mode&07777 != 0600 && st.Mode&07777 != 0400) {
				return errConfigCustody
			}
			if e = unix.Unlinkat(int(journal.Fd()), temp, 0); e != nil {
				return e
			}
		} else if !errors.Is(e, unix.ENOENT) {
			return e
		}
		if err = writeConfigFile(ctx, journal, temp, config, b.UID, b.GID, 0400, ops, "config"); err != nil {
			return err
		}
		if err = ops.check(ctx, "config-rename"); err != nil {
			return err
		}
		if err = unix.Renameat2(int(journal.Fd()), temp, int(target.Fd()), "wp-config.php", unix.RENAME_NOREPLACE); err != nil {
			return err
		}
	}
	if err = ops.check(ctx, "target-dir-sync"); err != nil {
		return err
	}
	if err = target.Sync(); err != nil {
		return err
	}
	if err = ops.check(ctx, "custody-dir-sync"); err != nil {
		return err
	}
	return journal.Sync()
}

// Walk from / with O_NOFOLLOW, retaining each ancestor until its child is open.
func openConfigDir(path string, owner uint32, site *ConfigBinding, fixture bool) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return nil, errConfigCustody
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	current := os.NewFile(uintptr(fd), "/")
	for i, part := range parts {
		next, e := unix.Openat(int(current.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		current.Close()
		if e != nil {
			return nil, e
		}
		current = os.NewFile(uintptr(next), part)
		var st unix.Stat_t
		if unix.Fstat(next, &st) != nil {
			current.Close()
			return nil, errConfigCustody
		}
		ok := st.Uid == owner && st.Mode&0022 == 0
		if fixture && i < len(parts)-2 {
			ok = true
		}
		if site != nil && i == len(parts)-2 {
			ok = st.Uid == site.UID && st.Mode&07777 == 0751
		}
		if site != nil && i == len(parts)-1 {
			ok = st.Uid == site.UID && st.Mode&07777 == 02750
		}
		if !ok {
			current.Close()
			return nil, errConfigCustody
		}
	}
	return current, nil
}

func readConfigFile(dir *os.File, name string, uid, gid uint32, mode uint32, checkGroup bool) ([]byte, bool, error) {
	fd, e := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(e, unix.ENOENT) {
		return nil, false, nil
	}
	if e != nil {
		return nil, false, e
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != mode || st.Uid != uid || (checkGroup && st.Gid != gid) || st.Nlink != 1 || st.Size > 2<<20 {
		return nil, true, errConfigCustody
	}
	raw, e := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	return raw, true, e
}

func writeConfigFile(ctx context.Context, dir *os.File, name string, data []byte, uid, gid uint32, mode uint32, ops configOps, stage string) error {
	old, exists, e := readConfigFile(dir, name, uid, gid, mode, true)
	if e != nil {
		return e
	}
	if exists {
		defer clear(old)
		if !bytes.Equal(old, data) {
			return errConfigCustody
		}
		// Only exact, safe prior bytes are reusable. Sync via a fresh held descriptor.
		fd, e := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			return e
		}
		f := os.NewFile(uintptr(fd), name)
		defer f.Close()
		if e = ops.check(ctx, stage+"-sync"); e != nil {
			return e
		}
		return f.Sync()
	}
	fd, e := unix.Openat(int(dir.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(fd), name)
	complete := false
	defer func() {
		f.Close()
		if !complete {
			_ = unix.Unlinkat(int(dir.Fd()), name, 0)
		}
	}()
	if e = ops.check(ctx, stage+"-write"); e != nil {
		return e
	}
	if _, e = f.Write(data); e != nil {
		return e
	}
	// Fchown is unnecessary for same-owner nonroot fixtures and prohibited there.
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		return e
	}
	if st.Uid != uid || st.Gid != gid {
		if e = f.Chown(int(uid), int(gid)); e != nil {
			return e
		}
	}
	if e = unix.Fchmod(fd, mode); e != nil {
		return e
	}
	if e = ops.check(ctx, stage+"-sync"); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = ops.check(ctx, stage+"-close"); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	complete = true
	return nil
}
