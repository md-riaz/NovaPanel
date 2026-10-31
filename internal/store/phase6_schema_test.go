package store

import (
	"os"
	"strings"
	"testing"
)

func TestPhase6ProductionSchema(t *testing.T) {
	s := fresh(t)
	ddl, e := migrations.ReadFile("migrations/0007_phase6.sql")
	if e != nil {
		t.Fatal(e)
	}
	spec, e := os.ReadFile("../../docs/spec/02-data-model.md")
	if e != nil {
		t.Fatal(e)
	}
	section := strings.SplitN(string(spec), "## Phase 6", 2)[1]
	canonical := strings.SplitN(strings.SplitN(section, "```sql\n", 2)[1], "```", 2)[0]
	if !strings.Contains(string(ddl), canonical) {
		t.Fatal("production migration differs from canonical DDL")
	}
	assertPhase6Hashes(t, s)
	var n int
	if e = s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('repos','backups','fw_pending','webhook_deliveries')`).Scan(&n); e != nil || n != 4 {
		t.Fatal(n, e)
	}
	for _, q := range []string{`INSERT INTO repos(id,name,kind,role,location,created)VALUES('one','one','local','panel','/scratch/one',1)`, `INSERT INTO repos(id,name,kind,location,created)VALUES('site','site','s3','s3:bucket',1)`, `INSERT INTO fw_pending(id,snapshot_path,revert_unit,deadline)VALUES('pending','/scratch','unit',1)`, `INSERT INTO webhook_deliveries VALUES('hook',1)`} {
		if _, e = s.db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	for _, q := range []string{`INSERT INTO repos(id,name,kind,role,location,created)VALUES('two','two','local','panel','/scratch/two',1)`, `INSERT INTO repos(id,name,kind,location,created)VALUES('bad','bad','unknown','x',1)`, `INSERT INTO repos(id,name,kind,role,location,created)VALUES('bad','bad','local','unknown','x',1)`, `INSERT INTO backups(id,repo_id,target_kind,target_ref,state,created)VALUES('bad','missing','site','x','done',1)`, `INSERT INTO backups(id,repo_id,target_kind,target_ref,state,created)VALUES('bad','one','unknown','x','done',1)`} {
		if _, e = s.db.Exec(q); e == nil {
			t.Fatal("constraint accepted", q)
		}
	}
	var role, retention string
	if e = s.db.QueryRow("SELECT role,retention_json FROM repos WHERE id='site'").Scan(&role, &retention); e != nil || role != "site" || retention != `{"keep_daily":7,"keep_weekly":4,"keep_monthly":6}` {
		t.Fatal(role, retention, e)
	}
	if e = s.db.QueryRow("SELECT confirmed FROM fw_pending WHERE id='pending'").Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	if e = s.db.QueryRow("SELECT count(*) FROM repos WHERE id='one' AND role='panel'").Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if _, e = s.db.Exec(`INSERT INTO backups(id,repo_id,target_kind,target_ref,state,created)VALUES('b','one','panel','panel','done',1)`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec("DELETE FROM repos WHERE id='one'"); e == nil {
		t.Fatal("referenced repository deleted")
	}
}
