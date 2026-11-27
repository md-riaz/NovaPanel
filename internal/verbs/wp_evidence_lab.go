//go:build wordpress_offline_lab

package verbs

import (
	"novapanel/internal/wordpress"
	"novapanel/internal/wordpress/offline"
)

func wpLifetimeAvailable() bool { return true }

func wpEvidence() (wordpress.ArtifactEvidence, bool) {
	m := wordpress.ApprovedArtifacts()
	for _, url := range []string{m.CLI.URL, "https://downloads.wordpress.org/release/wordpress-7.1.4.zip", m.ChecksumsURL} {
		if _, err := offline.Resolve(url); err != nil {
			return wordpress.ArtifactEvidence{}, false
		}
	}
	// Lab-only synthetic signature metadata; fixture hashes are checked above.
	return wordpress.ArtifactEvidence{CLISHA256: m.CLI.SHA256, WordPressSHA256: m.WordPress.SHA256, ChecksumsSHA256: m.ChecksumsSHA256, SignatureVerified: true, SignerFingerprint: m.CLISignerFingerprint}, true
}
