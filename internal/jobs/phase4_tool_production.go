//go:build !wordpress_offline_lab

package jobs

import (
	"context"
	runner "novapanel/internal/exec"
	"novapanel/internal/wordpress"
)

func verifyWordPressTool(ctx context.Context) error {
	_, err := wordpress.VerifyTool(ctx, runner.Real{})
	return err
}
