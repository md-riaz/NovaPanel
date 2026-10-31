package configcustody

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
	"novapanel/internal/id"
	"novapanel/internal/secret"
)

// RemoveConfigRecord removes only authenticated recovery material. The operation
// owner must first prove durable successful terminality and exclude root writers.
// It never opens or deletes the live site configuration.
func RemoveConfigRecord(ctx context.Context, custody string, b ConfigBinding, seal *secret.Sealer) error {
	if b.UID == 0 || b.GID == 0 {
		return errConfigCustody
	}
	return removeConfigRecord(ctx, custody, b, seal, 0, configOps{})
}

// RemoveConfigRecordAuthorized persists terminal ownership after opening and
// authenticating a present record, but before unlink. Absence authorizes before
// directory sync. A failed open/authentication cannot retire a live binding.
func RemoveConfigRecordAuthorized(ctx context.Context, custody string, b ConfigBinding, seal *secret.Sealer, authorize func() error) error {
	if b.UID == 0 || b.GID == 0 || authorize == nil {
		return errConfigCustody
	}
	authorized := false
	ops := configOps{boundary: func(stage string) error {
		if !authorized && (stage == "cleanup-authorize" || stage == "cleanup-dir-sync") {
			if err := authorize(); err != nil {
				return err
			}
			authorized = true
		}
		return nil
	}}
	return removeConfigRecord(ctx, custody, b, seal, 0, ops)
}

func removeConfigRecord(ctx context.Context, custody string, b ConfigBinding, seal *secret.Sealer, owner uint32, ops configOps) (err error) {
	defer func() {
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			err = errConfigCustody
		}
	}()
	if seal == nil || !id.Valid(b.JobID) || !id.Valid(b.SiteID) || !id.Valid(b.DatabaseID) || !id.Valid(b.AccountID) {
		return errConfigCustody
	}
	if err = ops.check(ctx, "cleanup-open"); err != nil {
		return err
	}
	dir, err := openConfigDir(custody, owner, nil, ops.fixtureAncestors)
	if err != nil {
		return err
	}
	defer dir.Close()
	var ds unix.Stat_t
	if unix.Fstat(int(dir.Fd()), &ds) != nil || ds.Mode&07777 != 0700 {
		return errConfigCustody
	}
	name := b.JobID + ".sealed"
	fd, e := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil && !errors.Is(e, unix.ENOENT) {
		return e
	}
	if e == nil {
		f := os.NewFile(uintptr(fd), name)
		defer f.Close()
		var held unix.Stat_t
		if unix.Fstat(fd, &held) != nil || held.Mode&unix.S_IFMT != unix.S_IFREG || held.Mode&07777 != 0600 || held.Uid != owner || held.Nlink != 1 || held.Size > 2<<20 {
			return errConfigCustody
		}
		if err = ops.check(ctx, "cleanup-read"); err != nil {
			return err
		}
		raw, e := io.ReadAll(io.LimitReader(f, (2<<20)+1))
		if e != nil || len(raw) > 2<<20 {
			return errConfigCustody
		}
		var envelope secret.Sealed
		if json.Unmarshal(raw, &envelope) != nil {
			return errConfigCustody
		}
		plain, e := seal.Open(envelope)
		if e != nil {
			return errConfigCustody
		}
		defer clear(plain)
		var record configRecord
		if json.Unmarshal(plain, &record) != nil {
			return errConfigCustody
		}
		defer clear(record.Config)
		if record.Version != 1 || record.Binding != b || len(record.Config) == 0 {
			return errConfigCustody
		}
		if err = ops.check(ctx, "cleanup-unlink"); err != nil {
			return err
		}
		var current unix.Stat_t
		if unix.Fstatat(int(dir.Fd()), name, &current, unix.AT_SYMLINK_NOFOLLOW) != nil || held.Dev != current.Dev || held.Ino != current.Ino || held.Nlink != 1 || current.Nlink != 1 {
			return errConfigCustody
		}
		if err = ops.check(ctx, "cleanup-authorize"); err != nil {
			return err
		}
		// Authorization can perform a durable write. Recheck the directory entry
		// afterwards before unlink, under the caller's root-writer exclusion.
		if unix.Fstatat(int(dir.Fd()), name, &current, unix.AT_SYMLINK_NOFOLLOW) != nil || held.Dev != current.Dev || held.Ino != current.Ino || current.Nlink != 1 {
			return errConfigCustody
		}
		if err = unix.Unlinkat(int(dir.Fd()), name, 0); err != nil {
			return err
		}
	}
	if err = ops.check(ctx, "cleanup-dir-sync"); err != nil {
		return err
	}
	return dir.Sync()
}
