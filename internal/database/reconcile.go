package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/store"
	"novapanel/internal/validate"
)

func reconcileDecision(phase string, present, known, resource, live bool) string {
	if !known {
		return "conflict"
	}
	if live {
		return "leave"
	}
	switch phase {
	case "deleting":
		if present {
			return "drop"
		}
		return "remove"
	case "created":
		if resource {
			if present {
				return "leave"
			}
			return "conflict"
		}
		return "drop"
	case "reserved":
		if present {
			return "conflict"
		}
		if !resource {
			return "remove"
		}
		return "conflict"
	}
	return "conflict"
}

// ReconcileLock serializes with jobs by parent database ID and object namespace.
// The returned release must be called after the identity and marker are re-read.
type ReconcileLock func(context.Context, string, string, string) (func(), error)

// Reconcile inspects trusted MariaDB markers, leaving ambiguous or foreign
// state untouched and reporting it as a conflict. Other markers still converge.
func Reconcile(ctx context.Context, runner exec.Runner, s *store.Store, lock ReconcileLock) ([]string, error) {
	changed := []string{}
	if s == nil || lock == nil {
		return nil, errors.New("reconcile dependencies unavailable")
	}
	if err := Bootstrap(ctx, runner); err != nil {
		return changed, err
	}
	raw, err := runSQL(ctx, runner, "SELECT kind,name,obj_id FROM novapanel.managed_objects ORDER BY kind,name;")
	if err != nil {
		return changed, err
	}
	var conflicts bool
	lines := []string{}
	if raw != "" {
		lines = strings.Split(raw, "\n")
	}
	for _, line := range lines {
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			return nil, errors.New("invalid provenance listing")
		}
		kind, marker, objectID := parts[0], parts[1], parts[2]
		name := marker
		if kind == "user" {
			if !strings.HasSuffix(marker, "@127.0.0.1") {
				conflicts = true
				continue
			}
			name = strings.TrimSuffix(marker, "@127.0.0.1")
		}
		if (kind != "schema" && kind != "user") || !id.Valid(objectID) || (&validate.V{}).DBName(name) != nil || (kind == "user" && len(name) > 32) {
			conflicts = true
			continue
		}
		evidence, err := s.ProvenanceIdentity(ctx, kind, name, objectID)
		if err != nil {
			return changed, err
		}
		if !evidence.Known {
			conflicts = true
			continue
		}
		unlock, err := lock(ctx, evidence.DatabaseID, kind, name)
		if err != nil {
			return changed, err
		}
		didChange, err := reconcileOne(ctx, runner, s, kind, name, marker, objectID, evidence.DatabaseID)
		unlock()
		if didChange {
			changed = append(changed, kind+":"+marker)
		}
		if errors.Is(err, ErrConflict) {
			conflicts = true
			continue
		}
		if err != nil {
			return changed, err
		}
	}
	databases, err := s.ListDatabases(ctx)
	if err != nil {
		return changed, err
	}
	for _, d := range databases {
		_, users, err := s.DatabaseWithUsers(ctx, d.ID)
		if errors.Is(err, sql.ErrNoRows) {
			continue // A concurrent delete removed this database after the listing.
		}
		if err != nil {
			return changed, err
		}
		for _, u := range users {
			removed, err := reconcileOwner(ctx, runner, s, lock, "user", u.Username, u.ID, d.ID)
			if removed {
				changed = append(changed, "user:"+u.Username+"@127.0.0.1")
			}
			if errors.Is(err, ErrConflict) {
				conflicts = true
				continue
			}
			if err != nil {
				return changed, err
			}
		}
		removed, err := reconcileOwner(ctx, runner, s, lock, "schema", d.Name, d.ID, d.ID)
		if removed {
			changed = append(changed, "schema:"+d.Name)
		}
		if errors.Is(err, ErrConflict) {
			conflicts = true
			continue
		}
		if err != nil {
			return changed, err
		}
	}
	if conflicts {
		return changed, ErrConflict
	}
	return changed, nil
}

// reconcileOwner removes stale owners only when the external object is absent
// without a marker, or a different identity owns the marker. Live jobs retain responsibility.
func reconcileOwner(ctx context.Context, runner exec.Runner, s *store.Store, lock ReconcileLock, kind, name, objectID, dbID string) (bool, error) {
	if !id.Valid(objectID) || !id.Valid(dbID) || (&validate.V{}).DBName(name) != nil || (kind == "user" && len(name) > 32) {
		return false, ErrConflict
	}
	unlock, err := lock(ctx, dbID, kind, name)
	if err != nil {
		return false, err
	}
	defer unlock()
	evidence, err := s.ProvenanceIdentity(ctx, kind, name, objectID)
	if err != nil || !evidence.Resource || evidence.Live {
		return false, err
	}
	marker := name
	exists := fmt.Sprintf("SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s'", name)
	if kind == "user" {
		marker += "@127.0.0.1"
		exists = fmt.Sprintf("SELECT COUNT(*) FROM mysql.user WHERE User='%s' AND Host='127.0.0.1'", name)
	}
	state, err := runSQL(ctx, runner, fmt.Sprintf("SELECT (SELECT COUNT(*) FROM novapanel.managed_objects WHERE kind='%s' AND name='%s'),(%s),(SELECT COUNT(*) FROM novapanel.managed_objects WHERE kind='%s' AND name='%s' AND obj_id='%s');", kind, marker, exists, kind, marker, objectID))
	if err != nil {
		return false, err
	}
	mismatch := false
	switch state {
	case "1\t1\t0", "1\t0\t0":
		mismatch = true
	case "0\t0\t0":
	case "1\t1\t1", "1\t0\t1":
		return false, nil // Marker pass handles ownership and phase.
	case "0\t1\t0":
		return false, ErrConflict // Present without proof: never adopt or drop.
	default:
		return false, errors.New("invalid owner state")
	}
	if kind == "user" {
		u, err := s.DatabaseUserByID(ctx, objectID)
		if err != nil {
			return false, err
		}
		if u.DatabaseID != dbID || u.Username != name {
			return false, ErrConflict
		}
		if err := s.DeleteDatabaseUser(ctx, u); err != nil {
			return false, err
		}
		if mismatch {
			return true, ErrConflict
		}
		return true, nil
	}
	// Do not cascade away children whose external state was not proven absent.
	_, users, err := s.DatabaseWithUsers(ctx, objectID)
	if err != nil {
		return false, err
	}
	if len(users) != 0 {
		return false, ErrConflict
	}
	if err := s.DeleteDatabase(ctx, objectID, name); err != nil {
		return false, err
	}
	if mismatch {
		return true, ErrConflict
	}
	return true, nil
}

func reconcileOne(ctx context.Context, runner exec.Runner, s *store.Store, kind, name, marker, objectID, dbID string) (bool, error) {
	evidence, err := s.ProvenanceIdentity(ctx, kind, name, objectID)
	if err != nil {
		return false, err
	}
	if !evidence.Known || evidence.DatabaseID != dbID {
		return false, ErrConflict
	}
	exists := fmt.Sprintf("SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s'", name)
	if kind == "user" {
		exists = fmt.Sprintf("SELECT COUNT(*) FROM mysql.user WHERE User='%s' AND Host='127.0.0.1'", name)
	}
	state, err := runSQL(ctx, runner, fmt.Sprintf("SELECT COALESCE((SELECT phase FROM novapanel.managed_objects WHERE kind='%s' AND name='%s' AND obj_id='%s'),'none'),(%s);", kind, marker, objectID, exists))
	if err != nil {
		return false, err
	}
	p := strings.Split(state, "\t")
	if len(p) != 2 || (p[1] != "0" && p[1] != "1") {
		return false, errors.New("invalid provenance state")
	}
	if p[0] == "none" {
		return false, nil
	}
	if evidence.Success && !evidence.Resource && p[0] != "deleting" {
		return false, ErrConflict
	}
	switch reconcileDecision(p[0], p[1] == "1", evidence.Known, evidence.Resource, evidence.Live) {
	case "leave":
		return false, nil
	case "conflict":
		return false, ErrConflict
	case "drop":
		if p[0] == "created" {
			if _, err = runSQL(ctx, runner, fmt.Sprintf("UPDATE novapanel.managed_objects SET phase='deleting' WHERE kind='%s' AND name='%s' AND obj_id='%s' AND phase='created';", kind, marker, objectID)); err != nil {
				return false, err
			}
		}
		statement := fmt.Sprintf("DROP DATABASE IF EXISTS `%s`;", name)
		if kind == "user" {
			statement = fmt.Sprintf("DROP USER IF EXISTS '%s'@'127.0.0.1';", name)
		}
		if _, err = runSQL(ctx, runner, statement); err != nil {
			return false, err
		}
	}
	phase := p[0]
	if phase == "created" {
		phase = "deleting"
	}
	_, err = runSQL(ctx, runner, fmt.Sprintf("DELETE FROM novapanel.managed_objects WHERE kind='%s' AND name='%s' AND obj_id='%s' AND phase='%s';", kind, marker, objectID, phase))
	return err == nil, err
}
