package database

import (
	"context"
	"novapanel/internal/exec"
	"novapanel/internal/id"
	"os"
	"strings"
	"testing"
)

func TestMariaDBSocketCreate(t *testing.T) {
	if os.Getenv("NOVAPANEL_MARIADB_TEST") != "1" {
		t.Skip("requires isolated MariaDB fixture")
	}
	ctx := context.Background()
	identifier, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	db, user := "d_"+strings.ToLower(identifier[6:]), "u_"+strings.ToLower(identifier[6:])
	cleanup := func() {
		_, _, code, err := (exec.Real{}).Run(ctx, "/usr/bin/mariadb", []string{"--no-defaults", "--protocol=socket", "--user=root"}, exec.RunOpts{Stdin: strings.NewReader("DROP USER IF EXISTS '" + user + "'@'127.0.0.1'; DROP DATABASE IF EXISTS `" + db + "`;")})
		if err != nil || code != 0 {
			t.Errorf("cleanup failed: %v", err)
		}
	}
	if err := Create(ctx, exec.Real{}, db, user, "TestPass123"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	secondUser := "v_" + strings.ToLower(identifier[6:])
	if err := CreateUser(ctx, exec.Real{}, db, secondUser, "OtherPass789"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _, _, _ = (exec.Real{}).Run(ctx, "/usr/bin/mariadb", []string{"--no-defaults", "--protocol=socket", "--user=root"}, exec.RunOpts{Stdin: strings.NewReader("DROP USER IF EXISTS '" + secondUser + "'@'127.0.0.1';")})
	})
	if err := DeleteUser(ctx, exec.Real{}, secondUser); err != nil {
		t.Fatal(err)
	}
	if err := DeleteUser(ctx, exec.Real{}, secondUser); err != nil {
		t.Fatalf("repeat user delete: %v", err)
	}
	check := "SELECT COUNT(*) FROM mysql.user WHERE User='" + secondUser + "' AND Host='127.0.0.1';"
	checkOut, _, checkCode, checkErr := (exec.Real{}).Run(ctx, "/usr/bin/mariadb", []string{"--no-defaults", "--protocol=socket", "--user=root", "--batch", "--skip-column-names"}, exec.RunOpts{Stdin: strings.NewReader(check)})
	if checkErr != nil || checkCode != 0 || strings.TrimSpace(string(checkOut)) != "0" {
		t.Fatal("user deletion not verified")
	}
	if err := Rotate(ctx, exec.Real{}, user, "NewTestPass456"); err != nil {
		t.Fatal(err)
	}
	query := "SELECT authentication_string=PASSWORD('NewTestPass456') FROM mysql.global_priv JOIN mysql.user USING (Host,User) WHERE mysql.user.User='" + user + "' AND mysql.user.Host='127.0.0.1';"
	out, _, code, err := (exec.Real{}).Run(ctx, "/usr/bin/mariadb", []string{"--no-defaults", "--protocol=socket", "--user=root", "--batch", "--skip-column-names"}, exec.RunOpts{Stdin: strings.NewReader(query)})
	if err != nil || code != 0 || strings.TrimSpace(string(out)) != "1" {
		t.Fatalf("rotation not verified: code=%d error=%v output=%q", code, err, out)
	}
	if err := Create(ctx, exec.Real{}, db, user, "TestPass123"); err == nil {
		t.Fatal("unexpectedly adopted existing database")
	}
	if err := Delete(ctx, exec.Real{}, db, []string{user, secondUser}); err != nil {
		t.Fatal(err)
	}
	if err := Delete(ctx, exec.Real{}, db, []string{user, secondUser}); err != nil {
		t.Fatalf("repeat delete: %v", err)
	}
	query = "SELECT (SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='" + db + "')+(SELECT COUNT(*) FROM mysql.user WHERE User='" + user + "' AND Host='127.0.0.1');"
	out, _, code, err = (exec.Real{}).Run(ctx, "/usr/bin/mariadb", []string{"--no-defaults", "--protocol=socket", "--user=root", "--batch", "--skip-column-names"}, exec.RunOpts{Stdin: strings.NewReader(query)})
	if err != nil || code != 0 || strings.TrimSpace(string(out)) != "0" {
		t.Fatalf("delete not verified: code=%d error=%v output=%q", code, err, out)
	}
}
