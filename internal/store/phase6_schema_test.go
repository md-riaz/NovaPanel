package store

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
)

func TestPhase6ReservedSchemaFixture(t *testing.T) {
	s := fresh(t)
	ctx := context.Background()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('repos','backups','fw_pending','webhook_deliveries')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("deferred Phase 6 schema installed in production: %d %v", n, err)
	}
	ddl, err := os.ReadFile("testdata/0007_phase6.sql")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := os.ReadFile("../../docs/spec/02-data-model.md")
	if err != nil {
		t.Fatal(err)
	}
	section := strings.SplitN(string(spec), "## Phase 6", 2)[1]
	canonical := strings.SplitN(strings.SplitN(section, "```sql\n", 2)[1], "```", 2)[0]
	if !strings.Contains(string(ddl), canonical) {
		t.Fatal("reserved fixture differs from canonical Phase 6 DDL")
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error { _, e := tx.Exec(string(ddl)); return e }); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('repos','backups','fw_pending','webhook_deliveries')`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("incomplete Phase 6 fixture: %d %v", n, err)
	}
	if _, err := s.db.Exec(`INSERT INTO repos(id,name,kind,role,location,created) VALUES('one','one','local','panel','/scratch/one',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO repos(id,name,kind,role,location,created) VALUES('two','two','local','panel','/scratch/two',1)`); err == nil {
		t.Fatal("fixture permitted multiple panel repositories")
	}
}
