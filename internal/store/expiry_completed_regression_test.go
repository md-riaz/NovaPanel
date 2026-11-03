package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

func TestExpireOperationsPreservesCompletedClaimsAcrossBatches(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.clock = &authClock{now: 100}
	for i := 0; i < 102; i++ {
		id := fmt.Sprintf("expiry-regression-%03d", i)
		state := "done"
		if i == 101 {
			state = "running"
		}
		phase4Job(t, s.db.raw, id, state)
		phase4Exec(t, s.db.raw, "INSERT INTO phase4_operations(job_id,kind,checkpoint,identities_json,nonce,ciphertext,expires,updated) VALUES(?,'database_rotate','prepared','{}',X'01',X'02',50,1)", id)
		phase4Exec(t, s.db.raw, "INSERT INTO job_claims(job_id,claim_hash,nonce,ciphertext,expires) VALUES(?,'hash',X'03',X'04',999)", id)
	}
	for _, want := range []int{100, 2, 0} {
		n, err := s.ExpireOperations(ctx)
		if err != nil || n != want {
			t.Fatalf("batch got %d, %v; want %d", n, err, want)
		}
	}
	if n := phase4Count(t, s.db.raw, "SELECT count(*) FROM jobs WHERE id LIKE 'expiry-regression-%' AND state='done' AND updated=1"); n != 101 {
		t.Fatalf("completed jobs changed: %d", n)
	}
	if n := phase4Count(t, s.db.raw, "SELECT count(*) FROM job_claims WHERE job_id LIKE 'expiry-regression-%' AND claim_hash='hash' AND nonce=X'03' AND ciphertext=X'04' AND expires=999"); n != 101 {
		t.Fatalf("completed claims retained: %d", n)
	}
	if n := phase4Count(t, s.db.raw, "SELECT count(*) FROM phase4_operations WHERE job_id LIKE 'expiry-regression-%' AND checkpoint='expired' AND nonce IS NULL AND ciphertext IS NULL"); n != 102 {
		t.Fatalf("expired journals: %d", n)
	}
	if n := phase4Count(t, s.db.raw, "SELECT count(*) FROM jobs WHERE id='expiry-regression-101' AND state='failed' AND code='interrupted'"); n != 1 {
		t.Fatal("unfinished job not failed")
	}
}
