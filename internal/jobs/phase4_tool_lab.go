//go:build wordpress_offline_lab

package jobs

import (
	"context"
	"novapanel/internal/wordpress"
	"novapanel/internal/wordpress/offline"
)

func verifyWordPressTool(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := offline.Resolve(wordpress.ApprovedArtifacts().CLI.URL)
	return err
}
