package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestDurableOperationValidatesOptionalNames(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := OperationRequest{Kind: DatabaseCreate, DatabaseID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR"}
	j := Job{ID: "name-job", Verb: "database.create", CreatedBy: "owner", State: "queued", Args: []byte(`{}`)}
	for _, tc := range []struct{ name, user string }{{"mysql", "safe_user"}, {"unsafe`name", "safe_user"}, {"valid_name", "root'@'%"}, {"valid_name", "abcdefghijklmnopqrstuvwxyzabcdefg"}} {
		r := base
		r.DatabaseName = tc.name
		r.Username = tc.user
		if _, err = s.PrepareOperation(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "hash"}, r, []byte{1}, []byte{2}); !errors.Is(err, ErrSiteInvalid) {
			t.Fatalf("accepted %q/%q: %v", tc.name, tc.user, err)
		}
	}
	base.DatabaseName = "client_db"
	base.Username = "client_user"
	op, err := s.PrepareOperation(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "hash"}, base, []byte{1}, []byte{2})
	if err != nil {
		t.Fatal(err)
	}
	if op.Identity.Database.Name != "client_db" || op.Identity.Accounts[0].Username != "client_user" {
		t.Fatalf("lost names: %+v", op.Identity)
	}
}
