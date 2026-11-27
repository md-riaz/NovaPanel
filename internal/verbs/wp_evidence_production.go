//go:build !wordpress_offline_lab

package verbs

import (
	"context"

	runner "novapanel/internal/exec"
	"novapanel/internal/wordpress"
)

func wpEvidence() (wordpress.ArtifactEvidence, bool) {
	evidence, err := wordpress.VerifyTool(context.Background(), runner.Real{})
	return evidence, err == nil
}
