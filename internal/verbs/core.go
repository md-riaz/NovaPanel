// Package verbs registers the compiled-in Phase 0 verbs.
package verbs

import (
	"context"
	"novapanel/internal/agent"
	"novapanel/internal/rpc"
	"novapanel/internal/version"
)

type emptyArgs struct{}

func (*emptyArgs) Validate() error { return nil }
func init() {
	rpc.Register(rpc.Verb{Name: "ping", Public: true, ReadOnly: true, NewArgs: func() any { return &emptyArgs{} }, Run: ping})
	rpc.Register(rpc.Verb{Name: "agent.info", MinRole: rpc.Owner, ReadOnly: true, NewArgs: func() any { return &emptyArgs{} }, Run: agentInfo})
}
func ping(context.Context, *agent.Agent, any) (any, error) {
	return struct {
		Version string `json:"version"`
	}{version.Version}, nil
}
func agentInfo(_ context.Context, a *agent.Agent, _ any) (any, error) {
	uptime := (a.Now() - a.Started) / 1000
	if uptime < 0 {
		uptime = 0
	}
	return struct {
		Version   string       `json:"version"`
		Build     version.Info `json:"build"`
		Started   int64        `json:"started"`
		UptimeSec int64        `json:"uptime_sec"`
	}{version.Version, version.Get(), a.Started, uptime}, nil
}
