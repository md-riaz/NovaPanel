package backup

import (
	"errors"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// Repository contains public metadata only. Passwords belong in sealed storage.
type Repository struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Role          string `json:"role"`
	Location      string `json:"location"`
	RetentionJSON string `json:"retention_json"`
	Created       int64  `json:"created"`
}

var ErrRepository = errors.New("invalid repository")
var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ValidateRepository deliberately supports a narrow subset of restic locations.
// Remote locations must not contain embedded passwords, queries or fragments.
func ValidateRepository(r Repository) error {
	if !identifier.MatchString(r.ID) || !identifier.MatchString(r.Name) || (r.Role != "site" && r.Role != "panel") || len(r.Location) > 2048 || strings.ContainsFunc(r.Location, func(c rune) bool { return unicode.IsSpace(c) || unicode.IsControl(c) }) {
		return ErrRepository
	}
	switch r.Kind {
	case "local":
		if !safeAbsolute(r.Location) {
			return ErrRepository
		}
	case "sftp":
		if !strings.HasPrefix(r.Location, "sftp://") {
			return ErrRepository
		}
		u, e := url.Parse(r.Location)
		if e != nil || u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !safeAbsolute(u.Path) {
			return ErrRepository
		}
		if u.User != nil {
			if _, ok := u.User.Password(); ok || !identifier.MatchString(u.User.Username()) {
				return ErrRepository
			}
		}
	case "s3":
		if !strings.HasPrefix(r.Location, "s3:https://") {
			return ErrRepository
		}
		u, e := url.Parse(strings.TrimPrefix(r.Location, "s3:"))
		if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !safeAbsolute(u.Path) {
			return ErrRepository
		}
	default:
		return ErrRepository
	}
	return nil
}

func safeAbsolute(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p && p != "/" && !strings.ContainsAny(p, "\x00\r\n")
}
