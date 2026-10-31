package store

import "context"

// ResumeTLSJob is reserved for the exclusive queue startup owner, after validation.
// The journal and original input/authority must still match the validated row.
func (s *Store) ResumeTLSJob(ctx context.Context, j Job, now int64) (Job, error) {
	r, err := s.db.ExecContext(ctx, `UPDATE jobs SET state='running',code=NULL,error=NULL,finished=NULL,updated=? WHERE id=? AND verb=? AND object_key=? AND args_json=? AND actor=? AND role=? AND created_by=? AND effectful=? AND automation=? AND (state='running' OR (state='failed' AND code='interrupted')) AND (step='tls_committed' OR EXISTS (SELECT 1 FROM settings WHERE key='tls:publication:v1' AND json_valid(value_json) AND json_extract(value_json,'$.job_id')=jobs.id))`, now, j.ID, j.Verb, j.ObjectKey, string(j.Args), j.Actor, j.Role, j.CreatedBy, j.Effectful, j.Automation)
	if err = affected(r, err); err != nil {
		return Job{}, err
	}
	return s.GetJob(ctx, j.ID)
}
