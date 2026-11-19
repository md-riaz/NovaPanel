//go:build !wordpress_offline_lab

package verbs

import (
	"context"
	"testing"

	"novapanel/internal/agent"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
)

func TestWordPressInstallUnavailableBeforeEffects(t *testing.T) {
	_, err := wpInstall(context.Background(), &agent.Agent{}, &wpInstallArgs{})
	if e, ok := err.(*rpc.CodedError); !ok || e.Code != "unsupported" {
		t.Fatalf("admission: %v", err)
	}
	err = wpInstallExec(context.Background(), &agent.Agent{}, &jobs.Job{})
	if e, ok := err.(*rpc.CodedError); !ok || e.Code != "unsupported" {
		t.Fatalf("execution: %v", err)
	}
}
