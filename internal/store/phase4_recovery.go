package store

import "context"

// PendingOperations returns a bounded batch without adopting expired tombstones.
// Unknown journal kinds fail when the queue loads their typed identities.
func (s *Store) PendingOperations(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+jobColumns+" FROM jobs WHERE EXISTS(SELECT 1 FROM phase4_operations o WHERE o.job_id=jobs.id AND o.checkpoint NOT IN ('done','expired')) ORDER BY created,id LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
