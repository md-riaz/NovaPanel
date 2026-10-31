package firewall

import (
	"context"
	"errors"
	"time"
)

// VerifiedRestore consumes only descriptors admitted by snapshot custody. It must
// restore rules and PriorEnabled idempotently and durably record the ID-bound
// audit marker before returning nil. Descriptors are borrowed for this call and
// must not be retained or replaced by reopening paths. ConfinedRestore supplies
// an unwired implementation for explicitly admitted destination directories.
type VerifiedRestore func(JournalRecord, *VerifiedSnapshot) error

func verifiedRecovery(ctx context.Context, custody *SnapshotCustody, restore VerifiedRestore) (func(JournalRecord) error, error) {
	if custody == nil || restore == nil {
		return nil, errors.New("missing verified recovery adapter")
	}
	return func(r JournalRecord) error {
		s, err := custody.Verify(ctx, r)
		if err != nil {
			return err
		}
		err = restore(r, s)
		return errors.Join(err, s.Close())
	}, nil
}

// FireVerified retains Fire's deadline and replay protocol, but cannot call the
// consumer until custody verifies the journal-bound snapshot. Verification
// failure leaves durable reverting intent for same-ID replay.
func (j *Journal) FireVerified(ctx context.Context, pendingID string, now func() time.Time, custody *SnapshotCustody, restore VerifiedRestore) error {
	callback, err := verifiedRecovery(ctx, custody, restore)
	if err != nil {
		return err
	}
	return j.Fire(ctx, pendingID, now, callback)
}

// RevertVerified retains manual admission and interrupted-recovery retry rules.
// This internal gate does not enable public revert or provide runtime restoration.
func (j *Journal) RevertVerified(ctx context.Context, pendingID string, now func() time.Time, custody *SnapshotCustody, restore VerifiedRestore) error {
	callback, err := verifiedRecovery(ctx, custody, restore)
	if err != nil {
		return err
	}
	return j.Revert(ctx, pendingID, now, callback)
}
