package store

import "context"

// AttachedDatabases returns sorted persisted identities, never credentials.
// The caller holds the site lock; this read releases SQLite before any DB lock.
func (s *Store) AttachedDatabases(ctx context.Context, siteID string) ([]OperationDatabaseRecord, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,coalesce(site_id,''),name,engine,(SELECT id FROM db_users WHERE db_id=databases.id AND owner=1),created FROM databases WHERE site_id=? ORDER BY id", siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OperationDatabaseRecord{}
	for rows.Next() {
		var v OperationDatabaseRecord
		if err = rows.Scan(&v.ID, &v.SiteID, &v.Name, &v.Engine, &v.PrimaryUserID, &v.Created); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
