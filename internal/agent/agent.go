// Package agent holds dependencies without importing the RPC transport.
package agent

import (
	"novapanel/internal/config"
	"novapanel/internal/exec"
	"novapanel/internal/validate"
	"time"
)

type Clock interface{ Now() int64 }
type realClock struct{}

func (realClock) Now() int64 { return time.Now().UnixMilli() }

type Agent struct {
	Cfg      config.Config
	Exec     exec.Runner
	Validate *validate.V
	Clock    Clock
	Started  int64
}

func New() *Agent { return NewWithDependencies(config.Defaults(), exec.Real{}, &validate.V{}) }
func NewWithDependencies(cfg config.Config, runner exec.Runner, v *validate.V) *Agent {
	c := realClock{}
	return &Agent{Cfg: cfg, Exec: runner, Validate: v, Clock: c, Started: c.Now()}
}
func (a *Agent) Now() int64 {
	if a.Clock != nil {
		return a.Clock.Now()
	}
	return time.Now().UnixMilli()
}
