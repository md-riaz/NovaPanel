//go:build wordpress_offline_lab

// Package offline exposes pinned fixtures only to explicitly tagged lab builds.
package offline

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
)

//go:embed testdata/*
var fixtures embed.FS

var allowed = map[string]struct{ path, hash string }{
	"https://github.com/wp-cli/wp-cli/releases/download/v2.12.0/wp-cli-2.12.0.phar": {"testdata/wp-cli-2.12.0.phar", "ce34ddd838f7351d6759068d09793f26755463b4a4610a5a5c0a97b68220d85c"},
	"https://downloads.wordpress.org/release/wordpress-7.1.4.zip":                   {"testdata/wordpress-7.1.4.zip", "a147146c159362778ecbbbfb07bb20ddf4e85b130f971c824e656a8f938c5f87"},
	"https://api.wordpress.org/core/checksums/1.0/?version=7.1.4&locale=en_US":      {"testdata/checksums.json", "bb718bddf197e0098f05395dd9b228fad70b394926ee9d70a8c6a67cf20781e7"},
}

// Resolve returns verified fixture bytes for an exact approved URL; it never accesses the network.
func Resolve(url string) ([]byte, error) {
	entry, ok := allowed[url]
	if !ok {
		return nil, errors.New("offline: unknown fixture")
	}
	b, err := fixtures.ReadFile(entry.path)
	if err != nil || !matches(b, entry.hash) {
		return nil, errors.New("offline: invalid fixture")
	}
	return b, nil
}

func matches(b []byte, want string) bool {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]) == want
}
