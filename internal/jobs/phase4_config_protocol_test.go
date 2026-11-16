package jobs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"novapanel/internal/rpcerr"
	"novapanel/internal/wordpress/configcustody"
)

func configCode(err error) string {
	var c *rpcerr.CodedError
	if errors.As(err, &c) {
		return c.Code
	}
	return ""
}

func TestConfigIsolationProtocol(t *testing.T) {
	for _, mode := range []string{"duplicate", "concurrent", "panic-after", "panic-effect", "swallowed-error"} {
		t.Run(mode, func(t *testing.T) {
			var effects atomic.Int32
			err := runConfigIsolation(context.Background(), configcustody.ConfigBinding{}, func(ctx context.Context, _ configcustody.ConfigBinding, cb func(context.Context) error) error {
				switch mode {
				case "concurrent":
					var wg sync.WaitGroup
					for range 16 {
						wg.Add(1)
						go func() { defer wg.Done(); _ = cb(ctx) }()
					}
					wg.Wait()
				default:
					_ = cb(ctx)
					if mode == "duplicate" {
						_ = cb(ctx)
					}
					if mode == "panic-after" {
						panic("secret panic text")
					}
				}
				return nil
			}, func(context.Context) error {
				effects.Add(1)
				if mode == "panic-effect" {
					panic("secret effect panic")
				}
				if mode == "swallowed-error" {
					return errors.New("secret failure")
				}
				return nil
			})
			if configCode(err) != "interrupted" || effects.Load() != 1 {
				t.Fatalf("code=%s effects=%d", configCode(err), effects.Load())
			}
		})
	}
}

func TestConfigIsolationRevokesRetainedCallback(t *testing.T) {
	var retained func(context.Context) error
	var effects atomic.Int32
	err := runConfigIsolation(context.Background(), configcustody.ConfigBinding{}, func(ctx context.Context, _ configcustody.ConfigBinding, cb func(context.Context) error) error {
		retained = cb
		return cb(ctx)
	}, func(context.Context) error { effects.Add(1); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if retained(context.Background()) == nil || effects.Load() != 1 {
		t.Fatal("retained callback executed")
	}
}

func TestConfigIsolationDrainsAsyncCallback(t *testing.T) {
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	callbackDone := make(chan error, 1)
	var retained func(context.Context) error
	go func() {
		defer close(finished)
		err := runConfigIsolation(context.Background(), configcustody.ConfigBinding{}, func(ctx context.Context, _ configcustody.ConfigBinding, cb func(context.Context) error) error {
			retained = cb
			go func() { callbackDone <- cb(ctx) }()
			<-entered
			return nil
		}, func(ctx context.Context) error { close(entered); <-ctx.Done(); close(release); return ctx.Err() })
		if configCode(err) != "interrupted" {
			t.Errorf("code=%s", configCode(err))
		}
	}()
	<-release
	<-finished
	if <-callbackDone == nil {
		t.Fatal("async effect not canceled")
	}
	if retained(context.Background()) == nil {
		t.Fatal("callback not revoked")
	}
}

func TestConfigIsolationPreEffectFailuresStayTerminal(t *testing.T) {
	for _, panics := range []bool{false, true} {
		err := runConfigIsolation(context.Background(), configcustody.ConfigBinding{}, func(context.Context, configcustody.ConfigBinding, func(context.Context) error) error {
			if panics {
				panic("secret")
			}
			return errors.New("secret")
		}, func(context.Context) error { t.Fatal("unexpected effect"); return nil })
		if err == nil || configCode(err) == "interrupted" {
			t.Fatal("pre-effect failure made recoverable")
		}
	}
}
