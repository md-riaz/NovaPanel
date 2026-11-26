package verbs

import (
	"os"
	"path/filepath"

	"novapanel/internal/id"
	"novapanel/internal/rpc"
)

// Deploy identity and known_hosts files are held in this per-site state directory.
func removeDeployFiles(dbPath, siteID string) error {
	if !id.Valid(siteID) || !filepath.IsAbs(dbPath) {
		return rpc.Errf("invalid_argument", "Invalid deploy state path")
	}
	path := filepath.Join(filepath.Dir(dbPath), "deploy")
	resolved, err := filepath.EvalSymlinks(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if resolved != path {
		return rpc.Errf("precondition_failed", "Invalid deploy state directory")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := root.Lstat(siteID)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return rpc.Errf("precondition_failed", "Invalid deploy site directory")
	}
	siteRoot, err := root.OpenRoot(siteID)
	if err != nil {
		return err
	}
	defer siteRoot.Close()
	for _, name := range []string{"identity", "known_hosts"} {
		if err = siteRoot.Remove(name); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
