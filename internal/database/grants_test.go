package database

import (
	"strings"
	"testing"
)

func TestPhase4GrantPatternRefusals(t *testing.T) {
	db := "d_01arz3ndektsv4rrffq69g5fav"
	user := "u_01arz3ndektsv4rrffq69g5faw"
	invalid := []string{"", "mysql", "d_abc", "u_01arz3ndektsv4rrffq69g5fav", "d_81arz3ndektsv4rrffq69g5fav", "d_01arz3ndektsv4rrffq69g5fai", strings.ToUpper(db), db + "x", db[:27], "dX" + db[2:], db + "%", db + "\\", db + "`", db + "'; DROP DATABASE mysql; --", db + "\x00", db + "\n"}
	for _, name := range invalid {
		t.Run(name, func(t *testing.T) {
			got, err := RenderGrants(name, user)
			if err == nil || got != (Grants{}) {
				t.Fatalf("accepted database %q", name)
			}
			account := strings.Replace(name, "d_", "u_", 1)
			if name == "u_01arz3ndektsv4rrffq69g5fav" {
				account = db // An otherwise-valid generated DB name is not an account.
			}
			got, err = RenderGrants(db, account)
			if err == nil || got != (Grants{}) {
				t.Fatalf("accepted account %q", name)
			}
		})
	}
}

func TestPhase4GrantPatternEscaping(t *testing.T) {
	for _, tc := range []struct{ in, pattern, identifier string }{
		{`d_abc`, `d\_abc`, "`d_abc`"},
		{`a\b_%`, `a\\b\_\%`, "`a\\b_%`"},
		{"a`b", "a`b", "`a``b`"},
	} {
		if got := escapeGrantPattern(tc.in); got != tc.pattern {
			t.Fatalf("pattern %q: %q", tc.in, got)
		}
		if got := quoteIdentifier(tc.in); got != tc.identifier {
			t.Fatalf("identifier %q: %q", tc.in, got)
		}
	}
}

func TestPhase4GrantPattern(t *testing.T) {
	db := "d_01arz3ndektsv4rrffq69g5fav"
	user := "u_01arz3ndektsv4rrffq69g5faw"
	got, err := RenderGrants(db, user)
	if err != nil {
		t.Fatal(err)
	}
	want := "GRANT SELECT,INSERT,UPDATE,DELETE,CREATE,DROP,ALTER,INDEX,CREATE TEMPORARY TABLES,LOCK TABLES,REFERENCES ON `d\\_01arz3ndektsv4rrffq69g5fav`.* TO '" + user + "'@'127.0.0.1';"
	if got.Grant != want {
		t.Fatalf("grant %q, want %q", got.Grant, want)
	}
	if got.CreateDatabase != "CREATE DATABASE `"+db+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;" || got.DropDatabase != "DROP DATABASE `"+db+"`;" {
		t.Fatalf("identifier escaping mixed with grant escaping: %+v", got)
	}
}
