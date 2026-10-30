// Package agent holds dependencies without importing the RPC transport.
package agent

import "time"

type Clock interface{ Now() int64 }
type realClock struct{}

func (realClock) Now() int64 { return time.Now().UnixMilli() }

type Agent struct {
	Clock   Clock
	Started int64
}

func New() *Agent { c := realClock{}; return &Agent{Clock: c, Started: c.Now()} }
func (a *Agent) Now() int64 {
	if a.Clock != nil {
		return a.Clock.Now()
	}
	return time.Now().UnixMilli()
}
