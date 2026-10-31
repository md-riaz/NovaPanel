package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SQL constraints exercise the production migration; typed APIs have separate tests.
func TestPhase4SchemaContract(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		t.Run(fmt.Sprintf("upgrade_%t", upgrade), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "panel.db")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if s != nil {
					s.Close()
				}
			}()
			if n := phase4Count(t, s.db.raw, "SELECT count(*) FROM schema_migrations"); n != 6 {
				t.Fatalf("production migrations: %d", n)
			}
			if upgrade {
				phase4Exec(t, s.db.raw, "INSERT INTO settings VALUES('preserved','true')")
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				s = nil
				s, err = Open(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			fixture, err := os.ReadFile("testdata/phase4_candidate.sql")
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := os.ReadFile("../../docs/spec/02-data-model.md")
			if err != nil {
				t.Fatal(err)
			}
			section := strings.SplitN(string(canonical), "## Phase 4 databases, PHP limits and bounded recovery", 2)[1]
			ddl := strings.SplitN(strings.SplitN(section, "```sql", 2)[1], "```", 2)[0]
			if strings.TrimSpace(ddl) != strings.TrimSpace(string(fixture)) {
				t.Fatal("fixture differs from canonical DDL")
			}

			phase4Exec(t, s.db.raw, "INSERT INTO sites(id,name,type,docroot,created,updated) VALUES('s1','one','wordpress','/scratch/one',1,1),('s2','two','wordpress','/scratch/two',1,1)")
			phase4Exec(t, s.db.raw, "INSERT INTO site_php_limits(site_id) VALUES('s1')")
			var memory, upload, seconds int
			if err := s.db.raw.QueryRow("SELECT memory_mb,upload_mb,max_exec_sec FROM site_php_limits WHERE site_id='s1'").Scan(&memory, &upload, &seconds); err != nil {
				t.Fatal(err)
			}
			if memory != 256 || upload != 64 || seconds != 60 {
				t.Fatalf("defaults %d/%d/%d", memory, upload, seconds)
			}
			phase4Pair(t, s, "d1", "u1", "s1")
			phase4Pair(t, s, "d2", "u2", "s2")
			t.Run("deferred_failed_commit_explicit_rollback_same_connection", func(t *testing.T) {
				conn, err := s.db.raw.Conn(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				phase4Exec(t, conn, "BEGIN")
				phase4Exec(t, conn, "INSERT INTO databases(id,name,primary_user_id,created) VALUES('bad','d_bad','missing',1)")
				phase4Refuse(t, conn, "COMMIT", "FOREIGN KEY")
				// database/sql.Tx.Commit marks Tx done even when SQLite leaves this transaction active.
				phase4Exec(t, conn, "ROLLBACK")
				phase4Exec(t, conn, "BEGIN")
				phase4Exec(t, conn, "INSERT INTO settings VALUES('after_rollback','true')")
				phase4Exec(t, conn, "COMMIT")
				if phase4Count(t, conn, "SELECT count(*) FROM databases WHERE id='bad'") != 0 {
					t.Fatal("failed commit leaked")
				}
			})
			t.Run("primary_and_binding_constraints", func(t *testing.T) {
				phase4Exec(t, s.db.raw, "INSERT INTO db_users(id,db_id,username,created) VALUES('u2-secondary','d2','u_secondary',1)")
				for _, q := range []string{"UPDATE databases SET primary_user_id='u2-secondary' WHERE id='d1'", "UPDATE databases SET primary_user_id='missing' WHERE id='d1'", "DELETE FROM db_users WHERE id='u1'"} {
					conn, err := s.db.raw.Conn(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					phase4Exec(t, conn, "BEGIN")
					phase4Exec(t, conn, q)
					phase4Refuse(t, conn, "COMMIT", "FOREIGN KEY")
					phase4Exec(t, conn, "ROLLBACK")
					conn.Close()
				}
				phase4Refuse(t, s.db.raw, "INSERT INTO wordpress_bindings VALUES('s2','d1')", "FOREIGN KEY")
				phase4Exec(t, s.db.raw, "INSERT INTO wordpress_bindings VALUES('s1','d1')")
				phase4Refuse(t, s.db.raw, "UPDATE databases SET site_id=NULL WHERE id='d1'", "FOREIGN KEY")
				phase4Refuse(t, s.db.raw, "DELETE FROM databases WHERE id='d1'", "FOREIGN KEY")
				phase4Refuse(t, s.db.raw, "INSERT INTO db_users VALUES('bad','d1','u_bad','localhost',1)", "CHECK")
				phase4Exec(t, s.db.raw, "INSERT INTO db_users(id,db_id,username,created) VALUES('extra','d1','u_extra',1)")
				if phase4Count(t, s.db.raw, "SELECT count(*) FROM db_users WHERE id='extra' AND host='127.0.0.1'") != 1 {
					t.Fatal("host default")
				}
				phase4Exec(t, s.db.raw, "DELETE FROM db_users WHERE id='extra'")
			})
			t.Run("php_tuple_bounds_and_partial_update", func(t *testing.T) {
				for _, tuple := range []string{"31,1,1", "2049,1,1", "256,0,1", "2048,1025,1", "256,1,0", "256,1,601", "32,33,60"} {
					phase4Refuse(t, s.db.raw, "UPDATE site_php_limits SET (memory_mb,upload_mb,max_exec_sec)=("+tuple+") WHERE site_id='s1'", "CHECK")
				}
				phase4Refuse(t, s.db.raw, "UPDATE site_php_limits SET memory_mb=32 WHERE site_id='s1'", "CHECK")
				if phase4Count(t, s.db.raw, "SELECT count(*) FROM site_php_limits WHERE memory_mb=256 AND upload_mb=64 AND max_exec_sec=60") != 1 {
					t.Fatal("invalid partial update mutated row")
				}
				for _, tuple := range []string{"32,1,1", "2048,1024,600"} {
					phase4Exec(t, s.db.raw, "UPDATE site_php_limits SET (memory_mb,upload_mb,max_exec_sec)=("+tuple+") WHERE site_id='s1'")
				}
				phase4Exec(t, s.db.raw, "UPDATE site_php_limits SET max_exec_sec=59 WHERE site_id='s1'")
				if phase4Count(t, s.db.raw, "SELECT count(*) FROM site_php_limits WHERE memory_mb=2048 AND upload_mb=1024 AND max_exec_sec=59") != 1 {
					t.Fatal("partial update lost omitted values")
				}
			})
			t.Run("atomic_terminal_sql_and_consumed_claim_replay", func(t *testing.T) { phase4Atomic(t, s) })
			t.Run("standalone_delete_journal_survives_reopen", func(t *testing.T) {
				phase4Job(t, s.db.raw, "delete", "running")
				identities := `{"db_id":"d2","name":"d_generated","accounts":[{"id":"u2","username":"u_generated","host":"127.0.0.1"}]}`
				phase4Exec(t, s.db.raw, "INSERT INTO phase4_operations(job_id,kind,db_id,checkpoint,identities_json,expires,updated) VALUES('delete','database_delete','d2','prepared',?,100,1)", identities)
				phase4Refuse(t, s.db.raw, "DELETE FROM jobs WHERE id='delete'", "FOREIGN KEY")
				phase4Exec(t, s.db.raw, "BEGIN; DELETE FROM databases WHERE id='d2'; UPDATE jobs SET state='done' WHERE id='delete'; UPDATE phase4_operations SET checkpoint='done' WHERE job_id='delete'; COMMIT;")
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				s = nil
				s, err = Open(path)
				if err != nil {
					t.Fatal(err)
				}
				var got string
				if err := s.db.raw.QueryRow("SELECT identities_json FROM phase4_operations WHERE job_id='delete' AND checkpoint='done'").Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got != identities {
					t.Fatal("replay identities lost")
				}
				if phase4Count(t, s.db.raw, "SELECT count(*) FROM databases WHERE id='d2'") != 0 || phase4Count(t, s.db.raw, "SELECT count(*) FROM db_users WHERE id='u2'") != 0 || phase4Count(t, s.db.raw, "SELECT count(*) FROM job_claims WHERE job_id='delete'") != 0 {
					t.Fatal("delete postcondition")
				}
			})
			t.Run("indexed_unfinished_expiry_bounded_before_limit", func(t *testing.T) { phase4Expiry(t, s) })
			if upgrade && phase4Count(t, s.db.raw, "SELECT count(*) FROM settings WHERE key='preserved'") != 1 {
				t.Fatal("upgrade lost existing data")
			}
			if phase4Count(t, s.db.raw, "SELECT count(*) FROM schema_migrations") != 6 {
				t.Fatal("missing production migration")
			}
			var integrity string
			if err := s.db.raw.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
				t.Fatalf("integrity %s %v", integrity, err)
			}
			rows, err := s.db.raw.Query("PRAGMA foreign_key_check")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			if rows.Next() {
				t.Fatal("foreign key violations")
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type phase4SQL interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func phase4Exec(t *testing.T, db phase4SQL, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}
func phase4Count(t *testing.T, db phase4SQL, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func phase4Refuse(t *testing.T, db phase4SQL, q, reason string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q); err == nil || !strings.Contains(err.Error(), reason) {
		t.Fatalf("expected %s refusal for %s: %v", reason, q, err)
	}
}
func phase4Pair(t *testing.T, s *Store, db, user, site string) {
	t.Helper()
	err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO databases(id,site_id,name,primary_user_id,created) VALUES(?,?,?,?,1)", db, site, "d_"+db, user); err != nil {
			return err
		}
		_, err := tx.Exec("INSERT INTO db_users(id,db_id,username,created) VALUES(?,?,?,1)", user, db, "u_"+user)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
func phase4Job(t *testing.T, db phase4SQL, id, state string) {
	t.Helper()
	phase4Exec(t, db, "INSERT INTO jobs(id,verb,args_json,object_key,actor,created_by,state,created,updated) VALUES(?,'database.create','{}','db:fixture','fixture','owner-session',?,1,1)", id, state)
}

func phase4Atomic(t *testing.T, s *Store) {
	phase4Job(t, s.db.raw, "atomic", "running")
	phase4Exec(t, s.db.raw, "INSERT INTO phase4_operations(job_id,kind,db_id,checkpoint,identities_json,nonce,ciphertext,expires,updated) VALUES('atomic','database_create','d3','verified','{}',X'01',X'02',100,1)")
	publish := func(tx *sql.Tx) error {
		statements := []string{
			"INSERT INTO databases(id,name,primary_user_id,created) VALUES('d3','d_three','u3',1)",
			"INSERT INTO db_users(id,db_id,username,created) VALUES('u3','d3','u_three',1)",
			`UPDATE jobs SET state='done',result_json='{"db_id":"d3"}',finished=20 WHERE id='atomic'`,
			"INSERT INTO job_claims(job_id,claim_hash,nonce,ciphertext,expires) VALUES('atomic','hashed-token',X'03',X'04',620)",
			"UPDATE phase4_operations SET checkpoint='done',nonce=NULL,ciphertext=NULL WHERE job_id='atomic'",
		}
		for _, q := range statements {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		return nil
	}
	sentinel := fmt.Errorf("injected terminal rollback")
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		if err := publish(tx); err != nil {
			return err
		}
		return sentinel
	}); err != sentinel {
		t.Fatal(err)
	}
	for _, q := range []string{"SELECT count(*) FROM databases WHERE id='d3'", "SELECT count(*) FROM db_users WHERE id='u3'", "SELECT count(*) FROM job_claims WHERE job_id='atomic'", "SELECT count(*) FROM jobs WHERE id='atomic' AND (state!='running' OR result_json IS NOT NULL OR finished IS NOT NULL)"} {
		if phase4Count(t, s.db.raw, q) != 0 {
			t.Fatal("terminal rollback leaked", q)
		}
	}
	if phase4Count(t, s.db.raw, "SELECT count(*) FROM phase4_operations WHERE job_id='atomic' AND checkpoint='verified' AND nonce=X'01' AND ciphertext=X'02'") != 1 {
		t.Fatal("rollback lost recovery material")
	}
	if err := s.Tx(context.Background(), publish); err != nil {
		t.Fatal(err)
	}
	if phase4Count(t, s.db.raw, "SELECT count(*) FROM phase4_operations WHERE job_id='atomic' AND checkpoint='done' AND nonce IS NULL AND ciphertext IS NULL") != 1 {
		t.Fatal("terminal recovery cleanup")
	}
	if phase4Count(t, s.db.raw, "SELECT count(*) FROM job_claims WHERE job_id='atomic' AND expires=620") != 1 {
		t.Fatal("claim publication")
	}
	phase4Exec(t, s.db.raw, "DELETE FROM job_claims WHERE job_id='atomic'")
	// The future typed terminal method must gate replay by done in this same transaction.
	phase4Exec(t, s.db.raw, "INSERT INTO job_claims(job_id,claim_hash,nonce,ciphertext,expires) SELECT job_id,'hashed-token',X'03',X'04',620 FROM phase4_operations WHERE job_id='atomic' AND checkpoint NOT IN ('done','expired')")
	if phase4Count(t, s.db.raw, "SELECT count(*) FROM job_claims WHERE job_id='atomic'") != 0 {
		t.Fatal("recreated consumed claim")
	}
	for _, q := range []string{"UPDATE phase4_operations SET ciphertext=X'02' WHERE job_id='atomic'", "UPDATE phase4_operations SET nonce=X'01',ciphertext=X'02' WHERE job_id='atomic'", "UPDATE phase4_operations SET reconciled=2 WHERE job_id='atomic'"} {
		phase4Refuse(t, s.db.raw, q, "CHECK")
	}
}

func phase4Expiry(t *testing.T, s *Store) {
	for i := 0; i < 205; i++ {
		id := fmt.Sprintf("expiry-%03d", i)
		state, checkpoint, expiry := "done", "done", 1
		if i >= 103 {
			state, checkpoint, expiry = "running", "prepared", 50
		}
		phase4Job(t, s.db.raw, id, state)
		phase4Exec(t, s.db.raw, "INSERT INTO phase4_operations(job_id,kind,checkpoint,identities_json,expires,updated) VALUES(?,'database_rotate',?,'{}',?,1)", id, checkpoint, expiry)
	}
	phase4Job(t, s.db.raw, "future", "running")
	phase4Exec(t, s.db.raw, "INSERT INTO phase4_operations(job_id,kind,checkpoint,identities_json,expires,updated) VALUES('future','database_rotate','prepared','{}',101,1)")
	query := "SELECT job_id FROM phase4_operations INDEXED BY idx_phase4_operations_expiry WHERE expires <= ? AND checkpoint NOT IN ('done','expired') ORDER BY expires,job_id LIMIT 100"
	rows, err := s.db.raw.Query("EXPLAIN QUERY PLAN "+query, 100)
	if err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	for rows.Next() {
		var a, b, c int
		var detail string
		if err := rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if !strings.Contains(plan.String(), "idx_phase4_operations_expiry") || strings.Contains(plan.String(), "TEMP B-TREE") {
		t.Fatal(plan.String())
	}
	rows, err = s.db.raw.Query(query, 100)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(ids) != 100 || ids[0] != "expiry-103" || ids[99] != "expiry-202" {
		t.Fatalf("expiry selection: %v", ids)
	}
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.Exec("UPDATE phase4_operations SET checkpoint='expired',nonce=NULL,ciphertext=NULL WHERE job_id=?", id); err != nil {
				return err
			}
			if _, err := tx.Exec("UPDATE jobs SET state='failed',code='interrupted',error='Recovery expired' WHERE id=? AND state IN ('queued','running')", id); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if phase4Count(t, s.db.raw, "SELECT count(*) FROM jobs WHERE id LIKE 'expiry-%' AND state='done'") != 103 || phase4Count(t, s.db.raw, "SELECT count(*) FROM jobs WHERE id LIKE 'expiry-%' AND state='failed' AND code='interrupted'") != 100 || phase4Count(t, s.db.raw, "SELECT count(*) FROM jobs WHERE id LIKE 'expiry-%' AND state='running'") != 2 {
		t.Fatal("bounded expiry altered wrong jobs")
	}
	if phase4Count(t, s.db.raw, "SELECT count(*) FROM jobs WHERE id='future' AND state='running'") != 1 || phase4Count(t, s.db.raw, "SELECT count(*) FROM job_claims") != 0 {
		t.Fatal("expiry affected future job or published claim")
	}
}
