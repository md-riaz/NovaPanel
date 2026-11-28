package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const shipped0008Hash = "3963587f2cc0c4713f3dc2eaeebc416f0630ecb8aaa543fba023f857aadf8def"

func shippedV6(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "panel.db")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw, err := migrations.ReadFile("migrations/hashes.json")
	if err != nil {
		t.Fatal(err)
	}
	var hashes map[string]string
	if err = json.Unmarshal(raw, &hashes); err != nil {
		t.Fatal(err)
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") || name[:4] > "0008" {
			continue
		}
		b, e := migrations.ReadFile("migrations/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if fmt.Sprintf("%x", sha256.Sum256(b)) != hashes[name[:4]] {
			t.Fatal("fixture hash mismatch", name)
		}
		if _, e = db.Exec(string(b)); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec("INSERT INTO schema_migrations VALUES(?,?,1)", name[:4], hashes[name[:4]]); e != nil {
			t.Fatal(e)
		}
	}
	if hashes["0008"] != shipped0008Hash {
		t.Fatal("shipped 0008 changed")
	}
	phase4Job(t, db, "old", "running")
	phase4Exec(t, db, "INSERT INTO phase4_operations VALUES('old','database_create','site','db','prepared','{}',0,x'01',x'02',42,7)")
	return path
}

func TestMigration0009RealOpen(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		t.Run(fmt.Sprint(upgrade), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fresh.db")
			if upgrade {
				path = shippedV6(t)
			}
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if phase4Count(t, s.db.raw, "SELECT count(*) FROM schema_migrations") != 19 {
				t.Fatal("migration count")
			}
			var h string
			if err = s.db.raw.QueryRow("SELECT hash FROM schema_migrations WHERE version='0008'").Scan(&h); err != nil || h != shipped0008Hash {
				t.Fatalf("0008 hash %s %v", h, err)
			}
			if upgrade && phase4Count(t, s.db.raw, "SELECT count(*) FROM phase4_operations WHERE job_id='old' AND kind='database_create' AND site_id='site' AND db_id='db' AND checkpoint='prepared' AND identities_json='{}' AND reconciled=0 AND nonce=x'01' AND ciphertext=x'02' AND expires=42 AND updated=7") != 1 {
				t.Fatal("row changed")
			}
			for _, kind := range []string{"php_set", "php_site_create"} {
				phase4Job(t, s.db.raw, kind, "running")
				phase4Exec(t, s.db.raw, "INSERT INTO phase4_operations(job_id,kind,checkpoint,identities_json,expires,updated) VALUES(?,?,'prepared','{}',1,1)", kind, kind)
			}
			phase4Job(t, s.db.raw, "bad", "running")
			phase4Refuse(t, s.db.raw, "INSERT INTO phase4_operations(job_id,kind,checkpoint,identities_json,expires,updated) VALUES('bad','bad','prepared','{}',1,1)", "CHECK")
			if phase4Count(t, s.db.raw, "SELECT count(*) FROM pragma_foreign_key_check") != 0 {
				t.Fatal("foreign keys")
			}
		})
	}
}

func TestMigration0009HashAndRollback(t *testing.T) {
	for _, mode := range []string{"hash", "ddl"} {
		t.Run(mode, func(t *testing.T) {
			path := shippedV6(t)
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "hash" {
				phase4Exec(t, db, "UPDATE schema_migrations SET hash='tampered' WHERE version='0008'")
			} else {
				phase4Exec(t, db, "CREATE TABLE php_create_reservations(collision TEXT)")
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			s, err := Open(path)
			if err == nil {
				s.Close()
				t.Fatal("expected refusal")
			}
			if mode == "hash" && !strings.Contains(err.Error(), "migration 0008 database hash mismatch") {
				t.Fatal(err)
			}
			if mode == "ddl" && !strings.Contains(err.Error(), "migration 0009") {
				t.Fatal(err)
			}
			db, err = sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if phase4Count(t, db, "SELECT count(*) FROM schema_migrations WHERE version='0009'") != 0 || phase4Count(t, db, "SELECT count(*) FROM phase4_operations WHERE job_id='old' AND nonce=x'01' AND ciphertext=x'02'") != 1 || phase4Count(t, db, "SELECT count(*) FROM sqlite_master WHERE name='phase4_operations_new'") != 0 {
				t.Fatal("partial migration")
			}
			phase4Job(t, db, "refused", "running")
			phase4Refuse(t, db, "INSERT INTO phase4_operations(job_id,kind,checkpoint,identities_json,expires,updated) VALUES('refused','php_set','prepared','{}',1,1)", "CHECK")
		})
	}
}

func TestRetiredAccountNameMigrationPreservesHistory(t *testing.T) {
	path := shippedV6(t)
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.ReadFile("migrations/hashes.json")
	if err != nil {
		t.Fatal(err)
	}
	var hashes map[string]string
	if err = json.Unmarshal(raw, &hashes); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") || name[:4] < "0009" || name[:4] > "0013" {
			continue
		}
		b, e := migrations.ReadFile("migrations/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(b)); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec("INSERT INTO schema_migrations VALUES(?,?,1)", name[:4], hashes[name[:4]]); e != nil {
			t.Fatal(e)
		}
	}
	phase4Job(t, db, "old-account", "queued")
	phase4Exec(t, db, `INSERT INTO sites(id,name,type,docroot,created,updated) VALUES('old-site','example','wordpress','/sites/example/public',1,1)`)
	phase4Exec(t, db, `INSERT INTO site_account_provenance(site_id,account_name,uid,gid,allocating_job,evidence) VALUES('old-site','example',2000000,2000000,'old-account','reserved')`)
	phase4Exec(t, db, `DELETE FROM sites WHERE id='old-site'`)
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if phase4Count(t, s.db.raw, `SELECT count(*) FROM site_account_provenance WHERE site_id IS NULL AND account_name='example' AND uid=2000000 AND allocating_job='old-account'`) != 1 {
		t.Fatal("retired identity lost on upgrade")
	}
	phase4Job(t, s.db.raw, "new-account", "queued")
	phase4Exec(t, s.db.raw, `INSERT INTO sites(id,name,type,docroot,created,updated) VALUES('new-site','example','wordpress','/sites/example/public',1,1)`)
	err = s.Tx(context.Background(), func(tx *sql.Tx) error {
		uid, e := s.ReserveSiteAccountTx(context.Background(), tx, "new-site", "example", "new-account")
		if e == nil && uid != 2000001 {
			t.Fatalf("UID reused: %d", uid)
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if phase4Count(t, s.db.raw, "SELECT count(*) FROM pragma_foreign_key_check") != 0 {
		t.Fatal("foreign keys changed")
	}
}
