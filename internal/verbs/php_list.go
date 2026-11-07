package verbs

import (
	"context"
	"strings"

	"novapanel/internal/agent"
	"novapanel/internal/exec"
	"novapanel/internal/platform"
	"novapanel/internal/rpc"
)

func init() {
	rpc.Register(rpc.Verb{Name: "php.list", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "sites:read", NewArgs: func() any { return &emptyArgs{} }, Run: phpList})
}

func phpList(ctx context.Context, a *agent.Agent, _ any) (any, error) {
	versions := []string{}
	_, _, code, err := a.Exec.Run(ctx, "/usr/bin/php8.3", []string{"-v"}, exec.RunOpts{})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err == nil && code == 0 {
		out, _, code, err := a.Exec.Run(ctx, platform.Systemctl, []string{"show", "--property=LoadState", "--value", "php8.3-fpm.service"}, exec.RunOpts{})
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err == nil && code == 0 && strings.TrimSpace(string(out)) == "loaded" {
			versions = append(versions, "8.3")
		}
	}
	return struct {
		Versions []string `json:"versions"`
		Default  string   `json:"default"`
	}{versions, "8.3"}, nil
}
