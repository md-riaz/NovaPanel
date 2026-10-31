package store

import (
	"context"
	"encoding/json"
	"testing"
)

func TestOperationRejectsCorruptIdentity(t *testing.T) {
	for _, mutation := range []string{"missing_accounts", "wrong_name", "wrong_host", "duplicate_account", "wrong_primary", "trailing_json", "wrong_verb"} {
		t.Run(mutation, func(t *testing.T) {
			s, e := Open(t.TempDir() + "/db")
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			ctx := context.Background()
			j := Job{ID: "01K8WMXZF57CGNZKX1FZ38DZMA", Verb: "database.create", CreatedBy: "owner", State: "queued", Args: []byte(`{}`)}
			op, e := s.PrepareOperation(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "hash"}, OperationRequest{Kind: DatabaseCreate, DatabaseID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR"}, []byte{1}, []byte{2})
			if e != nil {
				t.Fatal(e)
			}
			switch mutation {
			case "missing_accounts":
				op.Identity.Accounts = nil
			case "wrong_name":
				op.Identity.Database.Name = "d_unmanaged"
			case "wrong_host":
				op.Identity.Accounts[0].Host = "%"
			case "duplicate_account":
				op.Identity.Accounts = append(op.Identity.Accounts, op.Identity.Accounts[0])
			case "wrong_primary":
				op.Identity.Database.PrimaryUserID = "01K8WMXZF57CGNZKX1FZ38DZMS"
			}
			raw, _ := json.Marshal(op.Identity)
			if mutation == "trailing_json" {
				raw = append(raw, []byte(` {}`)...)
			}
			phase4Exec(t, s.db.raw, "UPDATE phase4_operations SET identities_json=? WHERE job_id=?", string(raw), j.ID)
			if mutation == "wrong_verb" {
				phase4Exec(t, s.db.raw, "UPDATE jobs SET verb='dbuser.delete' WHERE id=?", j.ID)
			}
			if _, e = s.LoadOperation(ctx, j.ID); e == nil {
				t.Fatal("corrupt operation accepted")
			}
		})
	}
}

func TestOperationRejectsChangedAccountMembership(t *testing.T) {
	s, e := Open(t.TempDir() + "/db")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	p := DatabasePrimary{ID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR"}
	if e = s.CreateDatabasePrimary(ctx, p); e != nil {
		t.Fatal(e)
	}
	j := Job{ID: "01K8WMXZF57CGNZKX1FZ38DZMA", Verb: "database.delete", CreatedBy: "owner", State: "queued", Args: []byte(`{}`)}
	if _, e = s.PrepareOperation(ctx, j, JobClaim{}, OperationRequest{Kind: DatabaseDelete, DatabaseID: p.ID}, nil, nil); e != nil {
		t.Fatal(e)
	}
	phase4Exec(t, s.db.raw, "INSERT INTO db_users(id,db_id,username,host,created) VALUES(?,?,?,'127.0.0.1',0)", "01K8WMXZF57CGNZKX1FZ38DZMS", p.ID, "u_01k8wmxzf57cgnzkx1fz38dzms")
	if e = s.CheckOperation(ctx, j.ID); e == nil {
		t.Fatal("changed account membership accepted")
	}
	if _, e = s.StartJob(ctx, j.ID, 1); e != nil {
		t.Fatal(e)
	}
	if e = s.CheckpointOperation(ctx, j.ID, "prepared", "db_applied"); e != nil {
		t.Fatal(e)
	}
	if e = s.CompleteOperation(ctx, j.ID, nil); e == nil {
		t.Fatal("terminal publication accepted changed account membership")
	}
}
