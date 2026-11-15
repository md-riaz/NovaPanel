package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Build exact historical prefixes without editing their migration bytes or
// opening a current Store before the upgrade under test.
func phase6OldDatabase(t *testing.T, version int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "panel.db")
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec("PRAGMA foreign_keys=ON"); e != nil {
		t.Fatal(e)
	}
	entries, e := migrations.ReadDir("migrations")
	if e != nil {
		t.Fatal(e)
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	for _, entry := range entries {
		var v int
		name := entry.Name()
		if _, e = fmt.Sscanf(name, "%04d_", &v); e != nil || !strings.HasSuffix(name, ".sql") || v > version {
			continue
		}
		b, e := migrations.ReadFile("migrations/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(string(b)); e != nil {
			t.Fatal(e)
		}
		h := sha256.Sum256(b)
		if _, e = tx.Exec("INSERT INTO schema_migrations VALUES(?,?,1)", fmt.Sprintf("%04d", v), hex.EncodeToString(h[:])); e != nil {
			t.Fatal(e)
		}
	}
	for _, q := range []string{
		`INSERT INTO users(id,username,email,pw_phc,role,created,updated)VALUES('u','owner','a@example.test','hash','owner',1,2)`,
		`INSERT INTO events(id,ts,actor,verb,outcome)VALUES('e',1,'owner','old','ok')`,
		`INSERT INTO schedules(id,verb,args_json,cron,next_run)VALUES('s','update.check','{}','daily',99)`,
		`INSERT INTO sites(id,name,type,docroot,created,updated)VALUES('site','site','static','/scratch/site',1,2)`,
		`INSERT INTO domains(id,site_id,fqdn,created)VALUES('domain','site','example.test',1)`,
	} {
		if _, e = tx.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	if version >= 6 {
		for _, q := range []string{`INSERT INTO databases(id,site_id,name,created)VALUES('db','site','site_db',1)`, `INSERT INTO db_users(id,db_id,username,owner,created)VALUES('du','db','site_user',1,1)`} {
			if _, e = tx.Exec(q); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	return path
}

func TestPhase6MigrationUpgrade(t *testing.T) {
	for _, version := range []int{5, 6, 7} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			path := phase6OldDatabase(t, version)
			for range 2 {
				s, e := Open(path)
				if e != nil {
					t.Fatal(e)
				}
				for q, want := range map[string]string{"SELECT email FROM users WHERE id='u'": "a@example.test", "SELECT actor FROM events WHERE id='e'": "owner", "SELECT cron FROM schedules WHERE id='s'": "daily", "SELECT site_id FROM domains WHERE id='domain'": "site"} {
					var got string
					if e = s.db.QueryRow(q).Scan(&got); e != nil || got != want {
						t.Fatal(q, got, e)
					}
				}
				if version >= 6 {
					var got string
					if e = s.db.QueryRow("SELECT db_users.db_id FROM db_users JOIN databases ON databases.id=db_users.db_id WHERE databases.site_id='site' AND databases.name='site_db' AND db_users.username='site_user' AND db_users.owner=1").Scan(&got); e != nil || got != "db" {
						t.Fatal(got, e)
					}
				}
				assertPhase6Hashes(t, s)
				rows, e := s.db.Query("PRAGMA foreign_key_check")
				if e != nil {
					t.Fatal(e)
				}
				if rows.Next() {
					t.Fatal("foreign key violation")
				}
				rows.Close()
				if e = s.Close(); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}

func assertPhase6Hashes(t *testing.T, s *Store) {
	t.Helper()
	raw, e := migrations.ReadFile("migrations/hashes.json")
	if e != nil {
		t.Fatal(e)
	}
	var hashes map[string]string
	if e = json.Unmarshal(raw, &hashes); e != nil {
		t.Fatal(e)
	}
	rows, e := s.db.Query("SELECT version,hash FROM schema_migrations ORDER BY version")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var v, h string
		if e = rows.Scan(&v, &h); e != nil {
			t.Fatal(e)
		}
		if hashes[v] != h {
			t.Fatal(v, h)
		}
		n++
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	if n != 8 || len(hashes) != 8 {
		t.Fatal("migration inventory", n, len(hashes))
	}
}

func TestPhase6MigrationRefusal(t *testing.T) {
	for _, kind := range []string{"phase85", "legacy-phase6", "hash", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			version := 5
			if kind == "legacy-phase6" {
				version = 6
			}
			path := phase6OldDatabase(t, version)
			db, e := sql.Open("sqlite", path)
			if e != nil {
				t.Fatal(e)
			}
			query := `UPDATE schema_migrations SET hash='wrong' WHERE version='0005'`
			if kind == "phase85" {
				query = `CREATE INDEX idx_sessions_idle_deadline_id ON sessions(idle_deadline, id);CREATE INDEX idx_sessions_abs_deadline_id ON sessions(abs_deadline, id);INSERT INTO schema_migrations VALUES('0006','af263cda18b1affbe82be79095b32bdb5d0a835278db2284893f56ed56be24a0',1)`
			}
			if kind == "legacy-phase6" {
				query = `INSERT INTO schema_migrations VALUES('0007','405e1a61297ac2123ca905b1309be6cf9e34a01e7c34a976646298902102a055',1)`
			}
			if kind == "unknown" {
				query = `INSERT INTO schema_migrations VALUES('9999','unknown',1)`
			}
			if _, e = db.Exec(query); e != nil {
				t.Fatal(e)
			}
			db.Close()
			if s, e := Open(path); e == nil {
				s.Close()
				t.Fatal("conflicting history accepted")
			} else if kind != "unknown" && !strings.Contains(e.Error(), "database hash mismatch") {
				t.Fatal(e)
			}
			db, e = sql.Open("sqlite", path)
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			var n int
			if e = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN ('repos','fw_pending')").Scan(&n); e != nil || n != 0 {
				t.Fatal("partial upgrade", n, e)
			}
			var email string
			if e = db.QueryRow("SELECT email FROM users WHERE id='u'").Scan(&email); e != nil || email != "a@example.test" {
				t.Fatal(email, e)
			}
		})
	}
}

// The PR-only 0006 is a different historical schema, not an upgrade source.
func TestPhase6RejectsPRMigrationHistory(t *testing.T) {
	path := phase6OldDatabase(t, 5)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("testdata/pr_0006_phase4.sql")
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(fixture)
	if got := hex.EncodeToString(h[:]); got != "549740ffa8b38ce925b524ad82013b8df75ff97c0346b6f7b3679ef267639698" {
		t.Fatal(got)
	}
	if _, err = db.Exec(string(fixture)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO schema_migrations VALUES('0006',?,1)`, hex.EncodeToString(h[:])); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO databases(id,site_id,name,primary_user_id,created) VALUES('db','site','site_db','du',1); INSERT INTO db_users(id,db_id,username,created) VALUES('du','db','site_user',1)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(path); err == nil {
		s.Close()
		t.Fatal("PR-only history accepted")
	} else if !strings.Contains(err.Error(), "database hash mismatch") {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('repos','fw_pending')`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	var name string
	if err = db.QueryRow(`SELECT name FROM databases WHERE id='db'`).Scan(&name); err != nil || name != "site_db" {
		t.Fatal(name, err)
	}
}

func TestPhase6MigrationAtomicity(t *testing.T) {
	for _, kind := range []string{"ddl", "commit"} {
		t.Run(kind, func(t *testing.T) {
			path := phase6OldDatabase(t, 7)
			db, e := sql.Open("sqlite", path)
			if e != nil {
				t.Fatal(e)
			}
			q := `CREATE TABLE backups(collision TEXT)`
			if kind == "commit" {
				q = `CREATE TABLE fault_parent(id INTEGER PRIMARY KEY);CREATE TABLE fault_child(id INTEGER REFERENCES fault_parent(id) DEFERRABLE INITIALLY DEFERRED);CREATE TRIGGER fault AFTER INSERT ON schema_migrations WHEN NEW.version='0008' BEGIN INSERT INTO fault_child VALUES(1);END`
			}
			if _, e = db.Exec(q); e != nil {
				t.Fatal(e)
			}
			db.Close()
			if s, e := Open(path); e == nil {
				s.Close()
				t.Fatal("migration fault accepted")
			}
			db, e = sql.Open("sqlite", path)
			if e != nil {
				t.Fatal(e)
			}
			var n int
			if e = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN ('repos','fw_pending','webhook_deliveries','repos_one_panel')").Scan(&n); e != nil || n != 0 {
				t.Fatal("partial DDL survived", n, e)
			}
			if e = db.QueryRow("SELECT count(*) FROM schema_migrations WHERE version='0008'").Scan(&n); e != nil || n != 0 {
				t.Fatal("ledger survived", n, e)
			}
			if kind == "ddl" {
				_, e = db.Exec("DROP TABLE backups")
			} else {
				_, e = db.Exec("DROP TRIGGER fault")
			}
			if e != nil {
				t.Fatal(e)
			}
			db.Close()
			s, e := Open(path)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			assertPhase6Hashes(t, s)
		})
	}
}
