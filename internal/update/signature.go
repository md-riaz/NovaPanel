// Package update verifies signed manifests and stages updates offline.
// Unset or malformed release-build trust input disables production entry points.
package update

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"

	"novapanel/internal/rpcerr"
)

// verifySignature authenticates the exact bytes without inspecting JSON.
// Keep this seam unexported: no production caller may supply a downloaded key.
func verifySignature(public ed25519.PublicKey, manifest, encoded []byte) error {
	encoded = bytes.TrimSpace(encoded)
	// Go's base64 decoder ignores CR/LF even in strict mode.
	if len(encoded) != base64.StdEncoding.EncodedLen(ed25519.SignatureSize) || bytes.ContainsAny(encoded, "\r\n") {
		return rpcerr.Errf("invalid_argument", "invalid update signature")
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(string(encoded))
	if err != nil || len(public) != ed25519.PublicKeySize || !ed25519.Verify(public, manifest, signature) {
		return rpcerr.Errf("invalid_argument", "invalid update signature")
	}
	return nil
}
