package configcustody

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"novapanel/internal/id"
)

// CheckConfigRecordAbsent never removes an entry. An acknowledged cleanup must
// reject any reappearing entry, and sync absence even after an earlier unlink.
func CheckConfigRecordAbsent(ctx context.Context, custody, jobID string) error {
	return checkConfigRecordAbsent(ctx, custody, jobID, 0, configOps{})
}

func checkConfigRecordAbsent(ctx context.Context, custody, jobID string, owner uint32, ops configOps) (err error) {
	defer func() {
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			err = errConfigCustody
		}
	}()
	if !id.Valid(jobID) {
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
	var st unix.Stat_t
	if unix.Fstat(int(dir.Fd()), &st) != nil || st.Mode&07777 != 0700 {
		return errConfigCustody
	}
	if err = unix.Fstatat(int(dir.Fd()), jobID+".sealed", &st, unix.AT_SYMLINK_NOFOLLOW); !errors.Is(err, unix.ENOENT) {
		return errConfigCustody
	}
	if err = ops.check(ctx, "cleanup-dir-sync"); err != nil {
		return err
	}
	return dir.Sync()
}
