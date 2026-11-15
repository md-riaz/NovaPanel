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

// AbortObject identifies an external object by its durable resource ID.
type AbortObject struct{ Kind, Name, ID string }

// AbortCreate first marks all provably owned objects, then drops them. Markers
// remain until after the job's failed terminal state is durable.
func AbortCreate(ctx context.Context, runner exec.Runner, objects []AbortObject) error {
	var owned []AbortObject
	for _, o := range objects {
		if runner == nil || !id.Valid(o.ID) || (&validate.V{}).DBName(o.Name) != nil || (o.Kind != "schema" && o.Kind != "user") || (o.Kind == "user" && len(o.Name) > 32) {
			return errors.New("invalid database input")
		}
		marker := o.Name
		exists := fmt.Sprintf("SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s'", o.Name)
		if o.Kind == "user" {
			marker += "@127.0.0.1"
			exists = fmt.Sprintf("SELECT COUNT(*) FROM mysql.user WHERE User='%s' AND Host='127.0.0.1'", o.Name)
		}
		state, err := runSQL(ctx, runner, fmt.Sprintf("SELECT COALESCE((SELECT phase FROM novapanel.managed_objects WHERE kind='%s' AND name='%s'),'none'),(%s),COALESCE((SELECT obj_id FROM novapanel.managed_objects WHERE kind='%s' AND name='%s'),'');", o.Kind, marker, exists, o.Kind, marker))
		if err != nil {
			return err
		}
		p := strings.Split(state, "\t")
		if len(p) != 3 {
			return errors.New("invalid mariadb provenance response")
		}
		if p[2] != o.ID || p[0] == "none" {
			continue
		}
		// A reserved object is not proven ours and must never be dropped.
		if p[0] == "reserved" {
			continue
		}
		if p[0] != "created" && p[0] != "deleting" {
			return ErrConflict
		}
		if _, err = runSQL(ctx, runner, fmt.Sprintf("UPDATE novapanel.managed_objects SET phase='deleting' WHERE kind='%s' AND name='%s' AND obj_id='%s' AND phase IN ('created','deleting');", o.Kind, marker, o.ID)); err != nil {
			return err
		}
		owned = append(owned, o)
	}
	for _, o := range owned {
		statement := fmt.Sprintf("DROP DATABASE IF EXISTS `%s`;", o.Name)
		if o.Kind == "user" {
			statement = fmt.Sprintf("DROP USER IF EXISTS '%s'@'127.0.0.1';", o.Name)
		}
		if _, err := runSQL(ctx, runner, statement); err != nil {
			return err
		}
	}
	return nil
}
