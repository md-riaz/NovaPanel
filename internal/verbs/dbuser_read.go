package verbs

import (
	"context"
	"database/sql"
	"errors"

	"novapanel/internal/agent"
	"novapanel/internal/rpc"
)

func init() {
	rpc.Register(rpc.Verb{Name: "dbuser.list", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "databases:read", NewArgs: func() any { return &databaseIDArgs{} }, Run: dbuserList})
}

func dbuserList(ctx context.Context, a *agent.Agent, args any) (any, error) {
	if err := databaseReady(ctx, a.Exec); err != nil {
		return nil, err
	}
	_, users, err := a.Store.DatabaseWithUsers(ctx, args.(*databaseIDArgs).DBID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, rpc.Errf("not_found", "Database not found")
	}
	if err != nil {
		return nil, err
	}
	type user struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	result := make([]user, 0)
	for _, u := range users {
		if !u.Owner {
			result = append(result, user{ID: u.ID, Username: u.Username})
		}
	}
	return struct {
		Users []user `json:"users"`
	}{result}, nil
}
