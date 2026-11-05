package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/validate"
)

var ErrConflict = errors.New("database provenance conflict")

func runSQL(ctx context.Context, runner exec.Runner, statement string) (string, error) {
	out, _, code, err := runner.Run(ctx, "/usr/bin/mariadb", []string{"--no-defaults", "--protocol=socket", "--user=root", "--batch", "--skip-column-names"}, exec.RunOpts{Stdin: strings.NewReader(statement)})
	if err != nil || code != 0 {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("mariadb operation failed")
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

var bootstrapMu = make(chan struct{}, 1)
var ErrUnavailable = errors.New("MariaDB unavailable")
var ErrUnsafe = errors.New("MariaDB control schema is unsafe")

// Bootstrap verifies the trust root on every use, without repairing foreign state.
func Bootstrap(ctx context.Context, runner exec.Runner) error {
	// Worker and synchronous RPC contexts already carry their respective deadlines.
	// Only add a fallback for callers that did not supply one.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	select {
	case bootstrapMu <- struct{}{}:
		defer func() { <-bootstrapMu }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if runner == nil {
		return ErrUnavailable
	}
	exists, err := runSQL(ctx, runner, "SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='novapanel';")
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnavailable
	}
	if exists == "0" {
		if _, err = runSQL(ctx, runner, "CREATE DATABASE IF NOT EXISTS `novapanel`;"); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrUnavailable
		}
	} else if exists != "1" {
		return ErrUnsafe
	}
	// Check the entire shape and all privilege scopes, including wildcard DB grants,
	// table/column/routine grants and global grants. Roles are non-root principals too.
	shape, err := bootstrapShape(ctx, runner)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnavailable
	}
	if shape != "1:5:5:2:2:0:0:0" && shape != "0:0:0:0:0:0:0:0" {
		return ErrUnsafe
	}
	grants, err := runSQL(ctx, runner, `SELECT (
 (SELECT COUNT(*) FROM information_schema.USER_PRIVILEGES WHERE BINARY GRANTEE <> BINARY '''root''@''localhost''' AND PRIVILEGE_TYPE <> 'USAGE') +
 (SELECT COUNT(*) FROM mysql.db WHERE NOT (BINARY User = BINARY 'root' AND BINARY Host = BINARY 'localhost') AND 'novapanel' LIKE Db) +
 (SELECT COUNT(*) FROM mysql.tables_priv WHERE NOT (BINARY User = BINARY 'root' AND BINARY Host = BINARY 'localhost') AND Db='novapanel') +
 (SELECT COUNT(*) FROM mysql.columns_priv WHERE NOT (BINARY User = BINARY 'root' AND BINARY Host = BINARY 'localhost') AND Db='novapanel') +
 (SELECT COUNT(*) FROM mysql.procs_priv WHERE NOT (BINARY User = BINARY 'root' AND BINARY Host = BINARY 'localhost') AND Db='novapanel'));`)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnavailable
	}
	if grants != "0" {
		return ErrUnsafe
	}
	if shape == "0:0:0:0:0:0:0:0" {
		_, err = runSQL(ctx, runner, "CREATE TABLE IF NOT EXISTS `novapanel`.`managed_objects` (kind ENUM('schema','user') NOT NULL, name VARCHAR(190) NOT NULL, obj_id CHAR(26) NOT NULL, phase ENUM('reserved','created','deleting') NOT NULL, created_at BIGINT NOT NULL, PRIMARY KEY(kind,name));")
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrUnavailable
		}
		shape, err = bootstrapShape(ctx, runner)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrUnavailable
		}
		if shape != "1:5:5:2:2:0:0:0" {
			return ErrUnsafe
		}
	}
	return nil
}

func bootstrapShape(ctx context.Context, runner exec.Runner) (string, error) {
	return runSQL(ctx, runner, `SELECT CONCAT(
 (SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='novapanel' AND TABLE_NAME='managed_objects' AND TABLE_TYPE='BASE TABLE'),':',
 (SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA='novapanel' AND TABLE_NAME='managed_objects'),':',
 (SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA='novapanel' AND TABLE_NAME='managed_objects' AND IS_NULLABLE='NO' AND ((ORDINAL_POSITION=1 AND COLUMN_NAME='kind' AND COLUMN_TYPE='enum(''schema'',''user'')') OR (ORDINAL_POSITION=2 AND COLUMN_NAME='name' AND COLUMN_TYPE='varchar(190)') OR (ORDINAL_POSITION=3 AND COLUMN_NAME='obj_id' AND COLUMN_TYPE='char(26)') OR (ORDINAL_POSITION=4 AND COLUMN_NAME='phase' AND COLUMN_TYPE='enum(''reserved'',''created'',''deleting'')') OR (ORDINAL_POSITION=5 AND COLUMN_NAME='created_at' AND COLUMN_TYPE='bigint(20)'))),':',
 (SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA='novapanel' AND TABLE_NAME='managed_objects' AND INDEX_NAME='PRIMARY'),':',
 (SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA='novapanel' AND TABLE_NAME='managed_objects' AND INDEX_NAME='PRIMARY' AND ((SEQ_IN_INDEX=1 AND COLUMN_NAME='kind') OR (SEQ_IN_INDEX=2 AND COLUMN_NAME='name')) AND SUB_PART IS NULL),':',
 (SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA='novapanel' AND TABLE_NAME='managed_objects' AND INDEX_NAME<>'PRIMARY'),':',
 (SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE EVENT_OBJECT_SCHEMA='novapanel' AND EVENT_OBJECT_TABLE='managed_objects'),':',
 (SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA='novapanel' AND TABLE_NAME='managed_objects' AND (EXTRA<>'' OR GENERATION_EXPRESSION IS NOT NULL OR COLUMN_DEFAULT IS NOT NULL)));`)
}

func preflightManaged(ctx context.Context, runner exec.Runner, kind, name, owner string) error {
	if runner == nil || !id.Valid(owner) || (&validate.V{}).DBName(name) != nil {
		return errors.New("invalid database input")
	}
	marker := name
	exists := fmt.Sprintf("SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s'", name)
	if kind == "user" {
		marker += "@127.0.0.1"
		exists = fmt.Sprintf("SELECT COUNT(*) FROM mysql.user WHERE User='%s' AND Host='127.0.0.1'", name)
	} else if kind != "schema" {
		return errors.New("invalid database input")
	}
	state, err := runSQL(ctx, runner, fmt.Sprintf("SELECT COALESCE((SELECT phase FROM novapanel.managed_objects WHERE kind='%s' AND name='%s'),'none'),(%s),COALESCE((SELECT obj_id FROM novapanel.managed_objects WHERE kind='%s' AND name='%s'),'');", kind, marker, exists, kind, marker))
	if err != nil {
		return err
	}
	parts := strings.Split(state, "\t")
	if len(parts) != 3 {
		return errors.New("invalid mariadb provenance response")
	}
	if parts[0] == "none" && parts[1] == "0" || parts[2] == owner && ((parts[0] == "reserved" && parts[1] == "0") || (parts[0] == "created" && parts[1] == "1")) {
		return nil
	}
	return ErrConflict
}

// createManaged requires the caller to hold the database-name job lock.
func createManaged(ctx context.Context, runner exec.Runner, kind, name, op, create string, finish func() error) error {
	if runner == nil || !id.Valid(op) || (&validate.V{}).DBName(name) != nil || (kind != "schema" && kind != "user") {
		return errors.New("invalid database input")
	}
	marker := name
	exists := fmt.Sprintf("SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s'", name)
	if kind == "user" {
		marker += "@127.0.0.1"
		exists = fmt.Sprintf("SELECT COUNT(*) FROM mysql.user WHERE User='%s' AND Host='127.0.0.1'", name)
	}
	query := fmt.Sprintf("SELECT COALESCE((SELECT phase FROM novapanel.managed_objects WHERE kind='%s' AND name='%s'),'none'),(%s),COALESCE((SELECT obj_id FROM novapanel.managed_objects WHERE kind='%s' AND name='%s'),'');", kind, marker, exists, kind, marker)
	state, err := runSQL(ctx, runner, query)
	if err != nil {
		return err
	}
	parts := strings.Split(state, "\t")
	if len(parts) < 3 {
		return errors.New("invalid mariadb provenance response")
	}
	phase, present, owner := parts[0], parts[1], parts[2]
	if phase != "none" && owner != op {
		return ErrConflict
	}
	switch phase {
	case "none":
		if present != "0" {
			return ErrConflict
		}
		_, err = runSQL(ctx, runner, fmt.Sprintf("INSERT INTO novapanel.managed_objects(kind,name,obj_id,phase,created_at) VALUES('%s','%s','%s','reserved',%d);", kind, marker, op, time.Now().UnixMilli()))
		if err != nil {
			return err
		}
	case "reserved":
		if present != "0" {
			return ErrConflict
		}
	case "deleting":
		if err := managedDrop(ctx, runner, kind, name, op); err != nil {
			return err
		}
		return ErrConflict
	case "created":
		if present != "1" {
			return ErrConflict
		}
		if finish != nil {
			return finish()
		}
		return nil
	default:
		return ErrConflict
	}
	if _, err = runSQL(ctx, runner, create+";"); err != nil {
		return err
	}
	if finish != nil {
		if err = finish(); err != nil {
			return err
		}
	}
	_, err = runSQL(ctx, runner, fmt.Sprintf("UPDATE novapanel.managed_objects SET phase='created' WHERE kind='%s' AND name='%s' AND obj_id='%s' AND phase='reserved';", kind, marker, op))
	return err
}
