package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"

	"novapanel/internal/rpcerr"
)

// The destination is private staging, never an installation path. Callers discard
// it on failure; unauthenticated bytes must never be unpacked or executed.
func verifyArtifact(ctx context.Context, a Artifact, src io.Reader, dst io.Writer) error {
	expected, err := hex.DecodeString(a.SHA256)
	if err != nil || len(expected) != sha256.Size || a.Size <= 0 || a.Size > maxArtifactBytes {
		return rpcerr.Errf("invalid_argument", "invalid artifact metadata")
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), io.LimitReader(contextReader{ctx, src}, a.Size+1))
	if err != nil {
		return err
	}
	if n != a.Size || hex.EncodeToString(h.Sum(nil)) != hex.EncodeToString(expected) {
		return rpcerr.Errf("invalid_argument", "update artifact integrity mismatch")
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
