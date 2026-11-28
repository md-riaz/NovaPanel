package firewall

import (
	"context"
	"errors"
	"time"
)

// Retry calls fn until it returns nil, until it has failed attempts times (zero means no limit),
// or until ctx ends. The wait after a failure starts at first and doubles up to max.
// failed, when set, receives each error with the wait that follows it.
func Retry(ctx context.Context, attempts int, first, max time.Duration, fn func() error, failed func(err error, wait time.Duration)) error {
	wait := first
	for n := 1; ; n++ {
		err := fn()
		if err == nil {
			return nil
		}
		if attempts > 0 && n >= attempts {
			return err
		}
		if failed != nil {
			failed(err, wait)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(err, ctx.Err())
		case <-timer.C:
		}
		wait *= 2
		if wait > max {
			wait = max
		}
	}
}
