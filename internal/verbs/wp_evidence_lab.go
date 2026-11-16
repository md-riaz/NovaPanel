//go:build wordpress_offline_lab

package verbs

import (
	"novapanel/internal/wordpress"
	"novapanel/internal/wordpress/offline"
)

func wpEvidence() (wordpress.ArtifactEvidence, error) {
	m := wordpress.ApprovedArtifacts()
	for _, url := range []string{m.CLI.URL, m.WordPress.URL, m.ChecksumsURL} {
		if _, err := offline.Resolve(url); err != nil {
			return wordpress.ArtifactEvidence{}, err
		}
	}
	// Lab-only synthetic signature metadata; fixture hashes are checked above.
	return wordpress.ArtifactEvidence{CLISHA256: m.CLI.SHA256, WordPressSHA256: m.WordPress.SHA256, ChecksumsSHA256: m.ChecksumsSHA256, SignatureVerified: true, SignerFingerprint: m.CLISignerFingerprint}, nil
}
