package store

import "context"

// QueuedJobsPageSize bounds each scheduling scan.
const QueuedJobsPageSize = 200

// QueuedJob pairs a job with its SQLite admission-order cursor.
type QueuedJob struct {
	Job   Job
	RowID int64
}

type queuedJobScanner struct {
	scanner
	rowID *int64
}

func (s queuedJobScanner) Scan(dest ...any) error {
	return s.scanner.Scan(append(dest, s.rowID)...)
}

// QueuedJobsHighWater captures the admission boundary for one finite pass.
func (s *Store) QueuedJobsHighWater(ctx context.Context) (int64, error) {
	var rowID int64
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(rowid), 0) FROM jobs").Scan(&rowID)
	return rowID, err
}

// ListQueuedJobs returns currently queued jobs in admission order within a pass boundary.
func (s *Store) ListQueuedJobs(ctx context.Context, afterRowID, highWater int64) ([]QueuedJob, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+jobColumns+",rowid FROM jobs WHERE state='queued' AND rowid>? AND rowid<=? ORDER BY rowid LIMIT ?", afterRowID, highWater, QueuedJobsPageSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QueuedJob{}
	for rows.Next() {
		var entry QueuedJob
		entry.Job, err = scanJob(queuedJobScanner{rows, &entry.RowID})
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
