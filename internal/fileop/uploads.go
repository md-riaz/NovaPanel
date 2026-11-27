package fileop

import (
	"os"
	"path/filepath"
	"time"
)

const UploadTTL = time.Hour

// SweepUploads removes unreferenced abandoned staged regular files.
func SweepUploads(dir string, now time.Time, isReferenced func(string) bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if isReferenced(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.Mode().IsRegular() && info.ModTime().Before(now.Add(-UploadTTL)) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}
