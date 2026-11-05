package store

import (
	"context"
	"database/sql"
	"errors"
)

// ProvenanceIdentity is trusted SQLite evidence for one external marker.
// DatabaseID identifies the lock shared with the owning job.
type ProvenanceIdentity struct {
	Known, Resource, Live, Success bool
	DatabaseID                     string
}

func (s *Store) ProvenanceIdentity(ctx context.Context, kind, name, objID string) (ProvenanceIdentity, error) {
	var out ProvenanceIdentity
	var dbID string
	var err error
	switch kind {
	case "schema":
		err = s.db.QueryRowContext(ctx, `SELECT id FROM databases WHERE id=? AND name=?`, objID, name).Scan(&dbID)
	case "user":
		err = s.db.QueryRowContext(ctx, `SELECT db_id FROM db_users WHERE id=? AND username=? AND host='127.0.0.1'`, objID, name).Scan(&dbID)
	default:
		return out, errors.New("invalid provenance kind")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil {
		out.Known, out.Resource, out.DatabaseID = true, true, dbID
	}
	// Invalid JSON or a mismatched object key is not valid proof. Match both
	// resource ID and name, not merely a substring of arbitrary job arguments.
	rows, err := s.db.QueryContext(ctx, `SELECT state, coalesce(code,''), json_extract(args_json,'$.db_id') FROM jobs WHERE json_valid(args_json) AND object_key = 'db:' || json_extract(args_json,'$.db_id') AND ((?='schema' AND verb='database.create' AND json_extract(args_json,'$.db_id')=? AND json_extract(args_json,'$.name')=?) OR (?='user' AND verb IN ('database.create','dbuser.create') AND json_extract(args_json,'$.user_id')=? AND json_extract(args_json,'$.username')=?))`, kind, objID, name, kind, objID, name)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var state, code, jobDB string
		if err = rows.Scan(&state, &code, &jobDB); err != nil {
			return out, err
		}
		if out.DatabaseID != "" && out.DatabaseID != jobDB {
			return ProvenanceIdentity{}, errors.New("conflicting provenance identities")
		}
		out.Known, out.DatabaseID = true, jobDB
		if state == "done" {
			out.Success = true
		} else if state != "failed" || code == "interrupted" {
			out.Live = true
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	if out.Known {
		// Deletion and rotation share the parent key even when their args do not
		// carry the child ID. Leave all live parent operations to the worker.
		var count int
		if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE object_key=? AND state IN ('queued','running')`, "db:"+out.DatabaseID).Scan(&count); err != nil {
			return out, err
		}
		out.Live = out.Live || count != 0
	}
	return out, nil
}
