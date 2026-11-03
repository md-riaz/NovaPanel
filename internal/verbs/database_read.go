package verbs

import (
	"context"
	"novapanel/internal/agent"
	"novapanel/internal/rpc"
)

func init() {
	rpc.Register(rpc.Verb{Name: "database.list", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "databases:read", NewArgs: func() any { return &emptyArgs{} }, Run: databaseList})
}

type databaseSummary struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	SiteID        *string `json:"site_id"`
	Engine        string  `json:"engine"`
	PrimaryUserID string  `json:"primary_user_id"`
}

func databaseList(ctx context.Context, a *agent.Agent, _ any) (any, error) {
	rows, err := a.Store.ListDatabases(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]databaseSummary, 0, len(rows))
	for _, row := range rows {
		var site *string
		if row.SiteID != "" {
			site = &row.SiteID
		}
		out = append(out, databaseSummary{ID: row.ID, Name: row.Name, SiteID: site, Engine: row.Engine, PrimaryUserID: row.PrimaryUserID})
	}
	return struct {
		Databases []databaseSummary `json:"databases"`
	}{out}, nil
}
