package wordpress

import (
	"archive/tar"
	"errors"
	"path"
	"strings"
)

// validateTarEntry accepts only regular files and directories under wordpress/.
func validateTarEntry(h *tar.Header) (string, error) {
	if h == nil || !strings.HasPrefix(h.Name, "wordpress/") || strings.Contains(h.Name, "\\") || path.IsAbs(h.Name) {
		return "", errors.New("wordpress: unsafe archive entry")
	}
	name := strings.TrimSuffix(strings.TrimPrefix(h.Name, "wordpress/"), "/")
	if name == "" && h.Typeflag == tar.TypeDir {
		return "", nil
	}
	if name == "" || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || (h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA && h.Typeflag != tar.TypeDir) || h.Size < 0 || h.Size > 64<<20 {
		return "", errors.New("wordpress: unsafe archive entry")
	}
	return name, nil
}
