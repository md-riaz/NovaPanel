package firewall

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVerifiedRecoveryRefusesTamperThenReplays(t *testing.T) {
	for _, manual := range []bool{false, true} {
		name := "fire"
		if manual {
			name = "manual"
		}
		t.Run(name, func(t *testing.T) {
			c, root := custodyFixture(t)
			ctx := context.Background()
			digest, e := c.Put(ctx, custodyID, custodyInput())
			mustCustody(t, e)
			j, r := journalFixture(t)
			r.SnapshotSHA256 = digest
			mustCustody(t, j.Arm(ctx, r))
			file := filepath.Join(root, custodyID, "ufw/user.rules")
			original, e := os.ReadFile(file)
			mustCustody(t, e)
			mustCustody(t, os.WriteFile(file, []byte("tampered"), 0600))
			calls := 0
			var held *VerifiedSnapshot
			consume := func(got JournalRecord, s *VerifiedSnapshot) error {
				calls++
				held = s
				if got.ID != r.ID || got.PriorEnabled != r.PriorEnabled {
					t.Fatal("record changed")
				}
				b, e := io.ReadAll(s.files["ufw/user.rules"])
				mustCustody(t, e)
				if string(b) != string(original) {
					t.Fatal("unverified bytes")
				}
				return nil
			}
			invoke := func(j *Journal, sec int64) error {
				now := func() time.Time { return time.Unix(sec, 0) }
				if manual {
					return j.RevertVerified(ctx, r.ID, now, c, consume)
				}
				return j.FireVerified(ctx, r.ID, now, c, consume)
			}
			sec := int64(100)
			if manual {
				sec = 99
			}
			if e := invoke(j, sec); e == nil || calls != 0 {
				t.Fatal("tamper reached restore", e, calls)
			}
			state, e := j.read()
			mustCustody(t, e)
			if state.State != "reverting" {
				t.Fatal("recovery intent lost")
			}
			mustCustody(t, os.WriteFile(file, original, 0600))
			reopened, e := OpenJournal(j.root)
			mustCustody(t, e)
			defer reopened.Close()
			mustCustody(t, invoke(reopened, 101))
			if calls != 1 {
				t.Fatal("wrong restore count")
			}
			if len(held.files) != 0 || len(held.dirs) != 0 {
				t.Fatal("verified descriptors leaked")
			}
		})
	}
}

func TestVerifiedRecoveryRequiresDependenciesBeforeIntent(t *testing.T) {
	j, r := journalFixture(t)
	ctx := context.Background()
	mustCustody(t, j.Arm(ctx, r))
	c, _ := custodyFixture(t)
	now := func() time.Time { return time.Unix(100, 0) }
	if e := j.FireVerified(ctx, r.ID, now, nil, func(JournalRecord, *VerifiedSnapshot) error { t.Fatal("consumer called"); return nil }); e == nil {
		t.Fatal("nil custody accepted")
	}
	if e := j.RevertVerified(ctx, r.ID, now, c, nil); e == nil {
		t.Fatal("nil consumer accepted")
	}
	state, e := j.read()
	mustCustody(t, e)
	if state.State != "armed" {
		t.Fatal("missing dependency published intent")
	}
}

func TestVerifiedRecoveryConsumerFailureRetainsIntentAndCloses(t *testing.T) {
	c, _ := custodyFixture(t)
	ctx := context.Background()
	digest, e := c.Put(ctx, custodyID, custodyInput())
	mustCustody(t, e)
	j, r := journalFixture(t)
	r.SnapshotSHA256 = digest
	mustCustody(t, j.Arm(ctx, r))
	fault := errors.New("consumer marker failure")
	var held *os.File
	e = j.FireVerified(ctx, r.ID, func() time.Time { return time.Unix(100, 0) }, c, func(_ JournalRecord, s *VerifiedSnapshot) error { held = s.files["enabled"]; return fault })
	if !errors.Is(e, fault) {
		t.Fatal("lost consumer failure", e)
	}
	if _, e := held.Stat(); e == nil {
		t.Fatal("consumer error leaked descriptor")
	}
	state, e := j.read()
	mustCustody(t, e)
	if state.State != "reverting" {
		t.Fatal("consumed failed restore")
	}
	mustCustody(t, j.FireVerified(ctx, r.ID, func() time.Time { return time.Unix(101, 0) }, c, func(JournalRecord, *VerifiedSnapshot) error { return nil }))
	mustCustody(t, j.FireVerified(ctx, r.ID, func() time.Time { return time.Unix(102, 0) }, c, func(JournalRecord, *VerifiedSnapshot) error { t.Fatal("terminal consumer called"); return nil }))
}
