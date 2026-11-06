package verbs

import (
	"context"
	"novapanel/internal/agent"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

func init() {
	rpc.Register(rpc.Verb{Name: "database.list", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "databases:read", NewArgs: func() any { return &emptyArgs{} }, Run: databaseList})
}

func databaseList(ctx context.Context, a *agent.Agent, _ any) (any, error) {
	if err := databaseReady(ctx, a.Exec); err != nil {
		return nil, err
	}
	rows, err := a.Store.ListDatabases(ctx)
	if err != nil {
		return nil, err
	}
	return struct {
		Databases []store.Database `json:"databases"`
	}{rows}, nil
}
