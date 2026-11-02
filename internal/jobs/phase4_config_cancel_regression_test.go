package jobs

import (
	"context"
	"sync/atomic"
	"testing"

	"novapanel/internal/wordpress/configcustody"
)

func TestConfigIsolationCanceledBeforeCallbackRefusesEffect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var effects atomic.Int32
	err := runConfigIsolation(ctx, configcustody.ConfigBinding{}, func(ctx context.Context, _ configcustody.ConfigBinding, cb func(context.Context) error) error {
		cancel()
		return cb(ctx)
	}, func(context.Context) error {
		effects.Add(1)
		return nil
	})
	if err != context.Canceled || effects.Load() != 0 {
		t.Fatalf("err=%v effects=%d", err, effects.Load())
	}
}
