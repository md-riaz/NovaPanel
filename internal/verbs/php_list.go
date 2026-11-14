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
	available, err := phpRuntimeAvailable(ctx, a.Exec)
	if err != nil {
		return nil, err
	}
	if available {
		versions = append(versions, "8.3")
	}
	return struct {
		Versions []string `json:"versions"`
		Default  string   `json:"default"`
	}{versions, "8.3"}, nil
}

func phpRuntimeAvailable(ctx context.Context, runner exec.Runner) (bool, error) {
	_, _, code, err := runner.Run(ctx, "/usr/bin/php8.3", []string{"-v"}, exec.RunOpts{})
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil || code != 0 {
		return false, nil
	}
	out, _, code, err := runner.Run(ctx, platform.Systemctl, []string{"show", "--property=LoadState", "--value", "php8.3-fpm.service"}, exec.RunOpts{})
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	return err == nil && code == 0 && strings.TrimSpace(string(out)) == "loaded", nil
}
