package database

import (
	"context"
	"errors"
	"fmt"
	"novapanel/internal/exec"
	"novapanel/internal/validate"
	"strings"
)

func DeleteUser(ctx context.Context, runner exec.Runner, user string) error {
	if runner == nil || (&validate.V{}).DBName(user) != nil || len(user) > 32 {
		return errors.New("invalid database input")
	}
	statement := fmt.Sprintf("DROP USER IF EXISTS '%s'@'127.0.0.1';\n", user)
	_, _, code, err := runner.Run(ctx, "/usr/bin/mariadb", []string{"--no-defaults", "--protocol=socket", "--user=root", "--batch"}, exec.RunOpts{Stdin: strings.NewReader(statement)})
	if err != nil || code != 0 {
		return errors.New("mariadb operation failed")
	}
	return nil
}

// Delete removes only the named loopback accounts and database; safe to repeat.
func Delete(ctx context.Context, runner exec.Runner, db string, users []string) error {
	v := &validate.V{}
	if runner == nil || v.DBName(db) != nil {
		return errors.New("invalid database input")
	}
	for _, user := range users {
		if v.DBName(user) != nil || len(user) > 32 {
			return errors.New("invalid database input")
		}
	}
	var sql strings.Builder
	for _, user := range users {
		fmt.Fprintf(&sql, "DROP USER IF EXISTS '%s'@'127.0.0.1';\n", user)
	}
	fmt.Fprintf(&sql, "DROP DATABASE IF EXISTS `%s`;\n", db)
	_, _, code, err := runner.Run(ctx, "/usr/bin/mariadb", []string{"--no-defaults", "--protocol=socket", "--user=root", "--batch"}, exec.RunOpts{Stdin: strings.NewReader(sql.String())})
	if err != nil || code != 0 {
		return errors.New("mariadb operation failed")
	}
	return nil
}

// ErrRotateIndeterminate means ALTER USER was sent and may have taken effect.
var ErrRotateIndeterminate = errors.New("mariadb rotation outcome indeterminate")

func Rotate(ctx context.Context, runner exec.Runner, user, password string) error {
	if runner == nil || (&validate.V{}).DBName(user) != nil || len(user) > 32 || len(password) == 0 {
		return errors.New("invalid database input")
	}
	for _, c := range password {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return errors.New("invalid password")
		}
	}
	statement := fmt.Sprintf("ALTER USER '%s'@'127.0.0.1' IDENTIFIED BY '%s';\n", user, password)
	_, _, code, err := runner.Run(ctx, "/usr/bin/mariadb", []string{"--no-defaults", "--protocol=socket", "--user=root", "--batch"}, exec.RunOpts{Stdin: strings.NewReader(statement)})
	if err != nil || code != 0 {
		return ErrRotateIndeterminate
	}
	return nil
}

func CreateUser(ctx context.Context, runner exec.Runner, db, user, password string) error {
	v := &validate.V{}
	if runner == nil || v.DBName(db) != nil || v.DBName(user) != nil || len(user) > 32 || password == "" {
		return errors.New("invalid database input")
	}
	for _, c := range password {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return errors.New("invalid password")
		}
	}
	grantDB := strings.ReplaceAll(db, "_", `\_`)
	statement := fmt.Sprintf("CREATE USER '%s'@'127.0.0.1' IDENTIFIED BY '%s';\nGRANT SELECT,INSERT,UPDATE,DELETE,CREATE,DROP,ALTER,INDEX,CREATE TEMPORARY TABLES,LOCK TABLES,REFERENCES ON `%s`.* TO '%s'@'127.0.0.1';\n", user, password, grantDB, user)
	_, _, code, err := runner.Run(ctx, "/usr/bin/mariadb", []string{"--no-defaults", "--protocol=socket", "--user=root", "--batch"}, exec.RunOpts{Stdin: strings.NewReader(statement)})
	if err != nil || code != 0 {
		return errors.New("mariadb operation failed")
	}
	return nil
}

// Create applies the database and restricted loopback grant through the local socket.
// A conflict is returned without adopting an existing account.
func Create(ctx context.Context, runner exec.Runner, db, user, password string) error {
	v := &validate.V{}
	if runner == nil || v.DBName(db) != nil || v.DBName(user) != nil || len(user) > 32 || len(password) < 1 {
		return errors.New("invalid database input")
	}
	for _, c := range password {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return errors.New("invalid password")
		}
	}
	grantDB := strings.ReplaceAll(db, "_", `\_`)
	sql := fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;\nCREATE USER '%s'@'127.0.0.1' IDENTIFIED BY '%s';\nGRANT SELECT,INSERT,UPDATE,DELETE,CREATE,DROP,ALTER,INDEX,CREATE TEMPORARY TABLES,LOCK TABLES,REFERENCES ON `%s`.* TO '%s'@'127.0.0.1';\n", db, user, password, grantDB, user)
	_, _, code, err := runner.Run(ctx, "/usr/bin/mariadb", []string{"--no-defaults", "--protocol=socket", "--user=root", "--batch"}, exec.RunOpts{Stdin: strings.NewReader(sql)})
	if err != nil || code != 0 {
		return errors.New("mariadb operation failed")
	}
	return nil
}
