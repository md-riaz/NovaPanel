//go:build !wordpress_offline_lab

package verbs

import (
	"novapanel/internal/rpc"
	"novapanel/internal/wordpress"
)

func wpLifetimeAvailable() bool { return false }

func wpEvidence() (wordpress.ArtifactEvidence, error) {
	return wordpress.ArtifactEvidence{}, rpc.Errf("unsupported", "WordPress artifact staging is unavailable")
}
