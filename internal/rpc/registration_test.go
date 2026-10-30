package rpc

import (
	"context"
	"novapanel/internal/agent"
	"novapanel/internal/jobs"
	"testing"
)

func TestRegisterHandlerContracts(t *testing.T) {
	run := func(context.Context, *agent.Agent, any) (any, error) { return nil, nil }
	exec := func(context.Context, *agent.Agent, *jobs.Job) error { return nil }
	for _, tc := range []struct {
		name                              string
		slow, effectful, run, exec, valid bool
	}{
		{name: "sync", run: true, valid: true},
		{name: "slow", slow: true, run: true, exec: true, valid: true},
		{name: "effectful slow", slow: true, effectful: true, run: true, exec: true, valid: true},
		{name: "missing run"},
		{name: "slow missing exec", slow: true, run: true},
		{name: "exec without slow", run: true, exec: true},
		{name: "effectful sync", run: true, effectful: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := Registry
			Registry = map[string]Verb{}
			defer func() { Registry = old }()
			v := Verb{Name: "test", Slow: tc.slow, Effectful: tc.effectful, NewArgs: func() any { return &struct{}{} }}
			if tc.run {
				v.Run = run
			}
			if tc.exec {
				v.Exec = exec
			}
			panicked := false
			func() { defer func() { panicked = recover() != nil }(); Register(v) }()
			if panicked == tc.valid {
				t.Fatalf("valid=%v panicked=%v", tc.valid, panicked)
			}
		})
	}
}
