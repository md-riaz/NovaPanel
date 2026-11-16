//go:build wordpress_offline_lab && (!linux || !amd64)

package main

import "errors"

// shortcut: only Linux amd64 is supported, add reviewed filters before enabling another architecture.
func denyNetwork() error { return errors.New("offline lab unsupported on this platform") }
