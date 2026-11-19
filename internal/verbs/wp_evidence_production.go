//go:build !wordpress_offline_lab

package verbs

import (
	"novapanel/internal/wordpress"
)

func wpLifetimeAvailable() bool { return false }

// wpEvidence reports no approved artifacts in a production build: staging the
// signed WordPress payload needs the operation-scoped lifetime owner deferred to
// #90. The ok result is always false here, so wp.install refuses before any
// effect. Returning a bool (not an always-non-nil error) keeps staticcheck from
// flagging the caller's guard as an impossible nil comparison (SA4023).
func wpEvidence() (wordpress.ArtifactEvidence, bool) {
	return wordpress.ArtifactEvidence{}, false
}
