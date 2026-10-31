package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestPhase6MigrationInventory(t *testing.T) {
	raw, e := migrations.ReadFile("migrations/hashes.json")
	if e != nil {
		t.Fatal(e)
	}
	var declared map[string]string
	if e = json.Unmarshal(raw, &declared); e != nil {
		t.Fatal(e)
	}
	if _, probe := declared["9998"]; probe {
		if s, e := Open(":memory:"); e == nil {
			s.Close()
			t.Fatal("orphan declared hash accepted by actual migrate")
		}
		return
	}
	const ddl = "CREATE TABLE sentinel(id INTEGER);"
	sum := sha256.Sum256([]byte(ddl))
	hash := hex.EncodeToString(sum[:])
	for _, kind := range []string{"valid", "duplicate", "malformed", "missing-hash", "orphan", "modified-six", "modified-seven"} {
		t.Run(kind, func(t *testing.T) {
			source := fstest.MapFS{"migrations/0001_init.sql": &fstest.MapFile{Data: []byte(ddl)}}
			hashes := map[string]string{"0001": hash}
			switch kind {
			case "duplicate":
				source["migrations/0001_other.sql"] = &fstest.MapFile{Data: []byte(ddl)}
			case "malformed":
				source["migrations/1_bad.sql"] = &fstest.MapFile{Data: []byte(ddl)}
			case "missing-hash":
				delete(hashes, "0001")
			case "orphan":
				hashes["9998"] = "orphan"
			case "modified-six", "modified-seven":
				v := "0006"
				if kind == "modified-seven" {
					v = "0007"
				}
				source["migrations/"+v+"_phase.sql"] = &fstest.MapFile{Data: []byte("modified")}
				hashes[v] = hash
			}
			entries, e := fs.ReadDir(source, "migrations")
			if e != nil {
				t.Fatal(e)
			}
			e = validateMigrationInventory(source, entries, hashes)
			if (e == nil) != (kind == "valid") {
				t.Fatal(kind, e)
			}
		})
	}
}
