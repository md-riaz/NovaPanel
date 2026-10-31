package integration

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestFirewallTerminalMigratedProcess(t *testing.T) {
	for _, prefix := range []int{0, 6} {
		t.Run(fmt.Sprint(prefix), func(t *testing.T) { firewallTerminalMigratedProcess(t, prefix) })
	}
}

func buildTerminalPrefix(t *testing.T, repo, path string, prefix int) {
	t.Helper()
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
	entries, e := os.ReadDir(repo + "/internal/store/migrations")
	if e != nil {
		t.Fatal(e)
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	for _, entry := range entries {
		name := entry.Name()
		var n int
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		if _, e = fmt.Sscanf(name, "%04d_", &n); e != nil || n > prefix {
			continue
		}
		b, e := os.ReadFile(repo + "/internal/store/migrations/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(string(b)); e != nil {
			t.Fatal(e)
		}
		h := sha256.Sum256(b)
		if _, e = tx.Exec("INSERT INTO schema_migrations VALUES(?,?,1)", fmt.Sprintf("%04d", n), hex.EncodeToString(h[:])); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = tx.Exec("INSERT INTO settings VALUES('old-prefix','preserved')"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
}
