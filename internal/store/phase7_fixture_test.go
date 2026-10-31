package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This fixture exercises canonical DDL without assigning a production migration.
func TestPhase7CanonicalSchemaFixture(t *testing.T) {
	raw, err := os.ReadFile("../../docs/spec/02-data-model.md")
	if err != nil {
		t.Fatal(err)
	}
	section := strings.SplitN(string(raw), "## Phase 7 — integrations (DNS)", 2)
	if len(section) != 2 {
		t.Fatal("missing canonical section")
	}
	blocks := strings.SplitN(section[1], "```sql\n", 2)
	if len(blocks) != 2 {
		t.Fatal("missing SQL")
	}
	ddl := strings.SplitN(blocks[1], "```", 2)[0]
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "dns-fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA foreign_keys=ON;" + ddl); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO dns_zones VALUES ('zone','missing','example.com','provider')`); err == nil {
		t.Fatal("missing FK")
	}
	if _, err = db.Exec(`INSERT INTO integrations VALUES ('integration','cloudflare','fixture','{}',0); INSERT INTO dns_zones VALUES ('zone','integration','example.com','provider'); DELETE FROM integrations WHERE id='integration'`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = db.QueryRow("SELECT count(*) FROM dns_zones").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
