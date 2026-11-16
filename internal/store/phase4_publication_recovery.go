package store

import "context"

// PendingPublicationAudits reads committed jobs without applying the public
// status refusal. It never includes nonterminal effects for automatic replay.
func (s *Store) PendingPublicationAudits(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+jobColumns+" FROM jobs WHERE "+publicationPendingSQL+" ORDER BY id LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, e := scanJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
