package agent

import (
	"novapanel/internal/config"
	"novapanel/internal/exec"
	"novapanel/internal/validate"
	"testing"
)

func TestDependencies(t *testing.T) {
	cfg := config.Defaults()
	cfg.SocketPath = "/scratch/agent.sock"
	a := NewWithDependencies(cfg, exec.Real{}, &validate.V{})
	var runner exec.Runner = a.Exec
	if runner == nil || a.Validate == nil || a.Cfg.SocketPath != cfg.SocketPath || a.Clock == nil || a.Started == 0 {
		t.Fatalf("missing dependencies: %+v", a)
	}
}
