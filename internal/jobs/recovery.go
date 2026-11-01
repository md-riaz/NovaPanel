package jobs

import (
	"context"
	"errors"
	"novapanel/internal/rpcerr"
	"time"
)

var ErrRecoveryBlocked = errors.New("jobs: startup recovery unresolved")

// SetStartupRecovery is trusted wiring, installed before Run. It cannot be invoked by RPC.
// Prepare validates the journal before the queue acquires its original running job.
func (q *Queue) SetStartupRecovery(prepare func(context.Context) (*Job, error), resume func(context.Context, *Job) error) {
	q.startup, q.resume = prepare, resume
}

func (q *Queue) recoverPublication(ctx context.Context, j Job) error {
	unlock := q.locks.lock(j.ObjectKey)
	defer unlock()
	if err := q.healthy(); err != nil {
		return err
	}
	s, ok := q.store.(interface {
		ResumeTLSJob(context.Context, Job, int64) (Job, error)
	})
	if !ok {
		return errors.New("jobs: TLS recovery unavailable")
	}
	var err error
	j, err = s.ResumeTLSJob(ctx, j, q.clock.Now())
	if err != nil {
		return err
	}
	err = invoke(WithMetadata(ctx, metadata(j)), q.resume, &j)
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if e := q.finish(finishCtx, j, err); e != nil {
		return e
	}
	// Only unresolved publication blocks subsequent work. Resolved failure is terminal.
	if err != nil {
		var coded *rpcerr.CodedError
		if errors.As(err, &coded) && coded.Code == "interrupted" {
			return err
		}
	}
	return nil
}
