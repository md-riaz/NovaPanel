package store

import (
	"context"
	"testing"
)

func TestOperationChildrenAtomicJournalAndReplay(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	phase4Job(t, s.db.raw, "child-job", "running")
	phase4Exec(t, s.db.raw, `INSERT INTO phase4_operations(job_id,kind,checkpoint,identities_json,expires,updated) VALUES('child-job','site_update','prepared','{}',9999999999999,1)`)
	children := []OperationChild{{Ordinal: 0, Kind: "maintenance_on"}, {Ordinal: 1, Kind: "maintenance_off"}}
	if err = s.PrepareOperationChildren(ctx, "child-job", children); err != nil {
		t.Fatal(err)
	}
	if err = s.EngageOperationBracket(ctx, "child-job"); err != nil {
		t.Fatal(err)
	}
	phase4Exec(t, s.db.raw, `CREATE TRIGGER reject_child_event BEFORE INSERT ON job_events BEGIN SELECT RAISE(ABORT,'injected'); END`)
	if err = s.CommitOperationChild(ctx, "child-job", 0, "verified", "committed", ""); err == nil {
		t.Fatal("journal failure accepted")
	}
	got, err := s.OperationChildren(ctx, "child-job")
	if err != nil || got[0].Status != "pending" || !got[0].BracketEngaged {
		t.Fatalf("partial commit: %+v %v", got, err)
	}
	phase4Exec(t, s.db.raw, `DROP TRIGGER reject_child_event`)
	if err = s.CommitOperationChild(ctx, "child-job", 0, "verified", "committed", ""); err != nil {
		t.Fatal(err)
	}
	phase4Refuse(t, s.db.raw, `UPDATE phase4_operation_children SET checkpoint='prepared' WHERE job_id='child-job' AND ordinal=0`, "cannot rewind")
	phase4Refuse(t, s.db.raw, `UPDATE phase4_operation_children SET bracket_engaged=0 WHERE job_id='child-job' AND ordinal=0`, "cannot rewind")
	got, err = s.OperationChildren(ctx, "child-job")
	if err != nil || got[0].Status != "committed" || got[0].JournalRef == "" {
		t.Fatalf("commit: %+v %v", got, err)
	}
}
