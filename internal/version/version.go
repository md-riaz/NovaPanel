// Package version exposes link-time build metadata.
package version

import "runtime"

// Set with -ldflags '-X novapanel/internal/version.Version=...'.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
}

func Get() Info {
	return Info{Version: Version, Commit: Commit, BuildTime: BuildTime, GoVersion: runtime.Version()}
}
