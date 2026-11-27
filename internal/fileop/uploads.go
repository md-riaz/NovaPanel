package fileop

import (
	"os"
	"path/filepath"
	"time"
)

// SweepUploads removes abandoned staged regular files.
func SweepUploads(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err == nil && info.Mode().IsRegular() && info.ModTime().Before(now.Add(-time.Hour)) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}
