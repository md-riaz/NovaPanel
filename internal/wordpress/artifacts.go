package wordpress

import "novapanel/internal/rpcerr"

// Artifact pins are reviewed release decisions, not a discovery or download API.
type Artifact struct {
	Version, URL, SHA256 string
}
type Manifest struct {
	CLI, WordPress                                              Artifact
	Locale, CLISignerFingerprint, ChecksumsURL, ChecksumsSHA256 string
	ChecksumEntries                                             int
}

func ApprovedArtifacts() Manifest {
	return Manifest{
		CLI:       Artifact{"2.12.0", "https://github.com/wp-cli/wp-cli/releases/download/v2.12.0/wp-cli-2.12.0.phar", "ce34ddd838f7351d6759068d09793f26755463b4a4610a5a5c0a97b68220d85c"},
		WordPress: Artifact{"7.1.4", "https://downloads.wordpress.org/release/wordpress-7.1.4.zip", "a147146c159362778ecbbbfb07bb20ddf4e85b130f971c824e656a8f938c5f87"},
		Locale:    "en_US", CLISignerFingerprint: "63AF7AA15067C05616FDDD88A3A2E8F226F0BC06",
		ChecksumsURL:    "https://api.wordpress.org/core/checksums/1.0/?version=7.1.4&locale=en_US",
		ChecksumsSHA256: "bb718bddf197e0098f05395dd9b228fad70b394926ee9d70a8c6a67cf20781e7", ChecksumEntries: 3782,
	}
}

// ArtifactEvidence is public verification metadata supplied by the trusted custody
// owner. It is not proof that bytes were hashed, a signature checked, or files held.
// Never accept this struct as an attestation from RPC or site-controlled input.
type ArtifactEvidence struct {
	CLISHA256         string `json:"cli_sha256"`
	WordPressSHA256   string `json:"wordpress_sha256"`
	ChecksumsSHA256   string `json:"checksums_sha256"`
	SignatureVerified bool   `json:"signature_verified"`
	SignerFingerprint string `json:"signer_fingerprint"`
}

func ValidateArtifacts(e ArtifactEvidence) error {
	m := ApprovedArtifacts()
	if e.CLISHA256 != m.CLI.SHA256 || e.WordPressSHA256 != m.WordPress.SHA256 || e.ChecksumsSHA256 != m.ChecksumsSHA256 || !e.SignatureVerified || e.SignerFingerprint != m.CLISignerFingerprint {
		return rpcerr.Errf("agent_error", "WordPress artifact verification required")
	}
	return nil
}
