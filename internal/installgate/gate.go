// Package installgate serializes one install domain's admitted work and teardown.
package installgate

import (
	"errors"
	"sync"
	"sync/atomic"
)

var ErrClosed = errors.New("install domain admission closed")

// Gate serializes effects, checks emptiness around them, and closes admission
// before initiating drain. Drain must cancel and wait for admitted effects.
type Gate struct {
	mu      sync.Mutex
	closeMu sync.Mutex
	closing atomic.Bool
	closed  bool
}

func (g *Gate) check(empty func() error) error {
	if g.closing.Load() {
		return ErrClosed
	}
	return empty()
}

// Check performs one empty observation without admitting an effect.
func (g *Gate) Check(empty func() error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.check(empty)
}

// Do holds the gate across both observations and the synchronous effect.
func (g *Gate) Do(empty func() error, effect func() error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.check(empty); err != nil {
		return err
	}
	err := effect()
	return errors.Join(err, g.check(empty))
}

// Close retries failed drains or removal without reopening admission.
func (g *Gate) Close(drain, remove func() error) error {
	g.closeMu.Lock()
	defer g.closeMu.Unlock()
	g.closing.Store(true)
	if g.closed {
		return ErrClosed
	}
	if err := drain(); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := remove(); err != nil {
		return err
	}
	g.closed = true
	return nil
}
