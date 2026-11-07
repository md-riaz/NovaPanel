package platform

import (
	"context"
	"errors"
	runner "novapanel/internal/exec"
	"novapanel/internal/php"
	"novapanel/internal/rpcerr"
	"novapanel/internal/validate"
	"os"
	"path/filepath"
)

// RemoveSite removes only a validated site's home and matching dormant account.
func RemoveSite(ctx context.Context, r runner.Runner, sites, name, siteID string) error {
	if e := (&validate.V{}).SiteName(name); e != nil {
		return e
	}
	resolved, e := filepath.EvalSymlinks(sites)
	if e != nil {
		return e
	}
	if !filepath.IsAbs(sites) || resolved != sites {
		return rpcerr.Errf("invalid_argument", "Invalid site root")
	}
	root, e := os.OpenRoot(sites)
	if e != nil {
		return e
	}
	defer root.Close()
	if info, e := root.Lstat(name); e == nil && !info.IsDir() {
		return rpcerr.Errf("invalid_argument", "Invalid site home")
	} else if e != nil && !os.IsNotExist(e) {
		return e
	}
	if e = php.VerifyUser(ctx, r, sites, name, siteID); e != nil {
		// A missing account is a completed replay only when no home remains.
		// A home without provenance may belong to someone else.
		var coded *rpcerr.CodedError
		if errors.As(e, &coded) && coded.Code == "not_found" {
			if _, statErr := root.Lstat(name); os.IsNotExist(statErr) {
				return nil
			} else if statErr != nil {
				return statErr
			}
		}
		return e
	}
	if e = root.RemoveAll(name); e != nil {
		return e
	}
	return php.DeleteUser(ctx, r, sites, name, siteID)
}
