package database

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/validate"
)

func ManagedCreate(ctx context.Context, runner exec.Runner, dbID, userID, db, user, password string) error {
	if (&validate.V{}).DBName(db) != nil || (&validate.V{}).DBName(user) != nil || len(user) > 32 || !safePassword(password) {
		return errors.New("invalid database input")
	}
	for _, item := range []struct{ kind, name, owner string }{{"schema", db, dbID}, {"user", user, userID}} {
		if err := preflightManaged(ctx, runner, item.kind, item.name, item.owner); err != nil {
			return err
		}
	}
	if err := createManaged(ctx, runner, "schema", db, dbID, fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", db), nil); err != nil {
		return err
	}
	return ManagedCreateUser(ctx, runner, userID, db, user, password)
}

// ManagedRotate requires the caller to hold the database and user namespace locks.
func ManagedRotate(ctx context.Context, runner exec.Runner, user, userID, password string) error {
	if runner == nil || !id.Valid(userID) || (&validate.V{}).DBName(user) != nil || len(user) > 32 || !safePassword(password) {
		return errors.New("invalid database input")
	}
	query := fmt.Sprintf("SELECT COALESCE((SELECT phase FROM novapanel.managed_objects WHERE kind='user' AND name='%s@127.0.0.1'),'none'),(SELECT COUNT(*) FROM mysql.user WHERE User='%s' AND Host='127.0.0.1'),COALESCE((SELECT obj_id FROM novapanel.managed_objects WHERE kind='user' AND name='%s@127.0.0.1'),'');", user, user, user)
	state, err := runSQL(ctx, runner, query)
	if err != nil {
		return err
	}
	if state != "created\t1\t"+userID {
		return ErrConflict
	}
	return Rotate(ctx, runner, user, password)
}

// ManagedDelete retains deleting markers for reconciliation after the job terminalizes.
func ManagedDelete(ctx context.Context, runner exec.Runner, db, dbID string, users map[string]string) error {
	for user, userID := range users {
		if err := managedDrop(ctx, runner, "user", user, userID); err != nil {
			return err
		}
	}
	return managedDrop(ctx, runner, "schema", db, dbID)
}
func ManagedDeleteUser(ctx context.Context, runner exec.Runner, user, userID string) error {
	return managedDrop(ctx, runner, "user", user, userID)
}
func managedDrop(ctx context.Context, runner exec.Runner, kind, name, objID string) error {
	if runner == nil || !id.Valid(objID) || (&validate.V{}).DBName(name) != nil || (kind != "user" && kind != "schema") {
		return errors.New("invalid database input")
	}
	marker := name
	statement := fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name)
	if kind == "user" {
		if len(name) > 32 {
			return errors.New("invalid database input")
		}
		marker += "@127.0.0.1"
		statement = fmt.Sprintf("DROP USER IF EXISTS '%s'@'127.0.0.1'", name)
	}
	state, err := runSQL(ctx, runner, fmt.Sprintf("SELECT COALESCE((SELECT phase FROM novapanel.managed_objects WHERE kind='%s' AND name='%s' AND obj_id='%s'),'none');", kind, marker, objID))
	if err != nil {
		return err
	}
	if state == "none" {
		return nil
	}
	if state != "created" && state != "deleting" {
		return ErrConflict
	}
	if _, err = runSQL(ctx, runner, fmt.Sprintf("UPDATE novapanel.managed_objects SET phase='deleting' WHERE kind='%s' AND name='%s' AND obj_id='%s';", kind, marker, objID)); err != nil {
		return err
	}
	if _, err = runSQL(ctx, runner, statement+";"); err != nil {
		return err
	}
	return nil
}

// ClearUserReservation discards only this job's uncreated reservation, never an object.
func ClearUserReservation(ctx context.Context, runner exec.Runner, user, userID string) error {
	if runner == nil || !id.Valid(userID) || (&validate.V{}).DBName(user) != nil || len(user) > 32 {
		return errors.New("invalid database input")
	}
	phase, err := runSQL(ctx, runner, fmt.Sprintf("SELECT COALESCE((SELECT phase FROM novapanel.managed_objects WHERE kind='user' AND name='%s@127.0.0.1' AND obj_id='%s'),'none');", user, userID))
	if err != nil {
		return err
	}
	if phase == "none" {
		return nil
	}
	if phase != "reserved" {
		return ErrConflict
	}
	state, err := runSQL(ctx, runner, fmt.Sprintf("SELECT COUNT(*) FROM mysql.user WHERE User='%s' AND Host='127.0.0.1';", user))
	if err != nil {
		return err
	}
	if state != "0" {
		return ErrConflict
	}
	_, err = runSQL(ctx, runner, fmt.Sprintf("DELETE FROM novapanel.managed_objects WHERE kind='user' AND name='%s@127.0.0.1' AND obj_id='%s' AND phase='reserved';", user, userID))
	return err
}

func safePassword(p string) bool {
	if p == "" {
		return false
	}
	for _, c := range p {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
func ManagedCreateUser(ctx context.Context, runner exec.Runner, op, db, user, password string) error {
	if (&validate.V{}).DBName(db) != nil || (&validate.V{}).DBName(user) != nil || len(user) > 32 || !safePassword(password) {
		return errors.New("invalid database input")
	}
	return createManaged(ctx, runner, "user", user, op, fmt.Sprintf("CREATE USER '%s'@'127.0.0.1' IDENTIFIED BY '%s'", user, password), func() error {
		if err := Rotate(ctx, runner, user, password); err != nil {
			return err
		}
		grantDB := strings.ReplaceAll(db, "_", `\_`)
		_, err := runSQL(ctx, runner, fmt.Sprintf("GRANT SELECT,INSERT,UPDATE,DELETE,CREATE,DROP,ALTER,INDEX,CREATE TEMPORARY TABLES,LOCK TABLES,REFERENCES ON `%s`.* TO '%s'@'127.0.0.1';", grantDB, user))
		return err
	})
}
