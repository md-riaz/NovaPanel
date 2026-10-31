package update

import (
	"context"
	"crypto/ed25519"
	"io"
	"net/http"
	"net/url"
	"novapanel/internal/rpcerr"
	"runtime"
)

// Enabled must precede queueing, filesystem inspection, or any network access.
func Enabled() error { _, err := releaseKey(); return err }
func Check(ctx context.Context, manifestURL, installed string) (Manifest, Artifact, error) {
	key, err := releaseKey()
	if err != nil {
		return Manifest{}, Artifact{}, err
	}
	return check(ctx, key, http.DefaultClient, manifestURL, installed)
}
func check(ctx context.Context, key ed25519.PublicKey, client *http.Client, raw, installed string) (Manifest, Artifact, error) {
	if len(key) != ed25519.PublicKeySize {
		return Manifest{}, Artifact{}, rpcerr.Errf("unsupported", "update trust anchor unavailable")
	}
	body, err := fetch(ctx, client, raw, maxManifestBytes)
	if err != nil {
		return Manifest{}, Artifact{}, err
	}
	u, _ := url.Parse(raw)
	u.Path += ".sig"
	u.RawPath = ""
	sig, err := fetch(ctx, client, u.String(), 1024)
	if err != nil {
		return Manifest{}, Artifact{}, err
	}
	return verifyManifest(key, body, sig, installed, runtime.GOOS, runtime.GOARCH)
}
func validURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == "" && u.Opaque == ""
}
func fetch(ctx context.Context, client *http.Client, raw string, limit int64) ([]byte, error) {
	if !validURL(raw) {
		return nil, rpcerr.Errf("invalid_argument", "invalid update URL")
	}
	safe := *client
	safe.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !validURL(req.URL.String()) {
			return rpcerr.Errf("invalid_argument", "invalid update redirect")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, rpcerr.Errf("agent_error", "update download failed")
	}
	res, err := safe.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, rpcerr.Errf("agent_error", "update download failed")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, rpcerr.Errf("agent_error", "update download failed")
	}
	body, err := io.ReadAll(io.LimitReader(contextReader{ctx, res.Body}, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, rpcerr.Errf("agent_error", "update download failed")
	}
	if int64(len(body)) > limit {
		return nil, rpcerr.Errf("invalid_argument", "update response exceeds limit")
	}
	return body, nil
}
