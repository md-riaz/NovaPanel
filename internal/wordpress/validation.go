package wordpress

import (
	"net/mail"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"novapanel/internal/id"
	"novapanel/internal/rpcerr"
	"novapanel/internal/validate"
)

var adminUsername = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,59}$`)

// Restrict configured roots as well as site names; no option-shaped components.
var rootPath = regexp.MustCompile(`^/[A-Za-z0-9_][A-Za-z0-9_.-]*(?:/[A-Za-z0-9_][A-Za-z0-9_.-]*)*$`)

func validateInput(in InstallInput) error {
	invalid := func(field string) error { return rpcerr.Errf("invalid_argument", "invalid WordPress %s", field) }
	v := &validate.V{}
	if !rootPath.MatchString(in.SitesRoot) || filepath.Clean(in.SitesRoot) != in.SitesRoot {
		return invalid("sites root")
	}
	if err := v.SiteName(in.SiteName); err != nil {
		return invalid("site name")
	}
	if err := v.Hostname(in.PrimaryDomain); err != nil {
		return invalid("primary domain")
	}
	if in.UID == 0 || in.GID == 0 {
		return invalid("site identity")
	}
	if !id.Valid(in.DatabaseID) || !id.Valid(in.DatabaseUserID) {
		return invalid("reserved database identity")
	}
	if in.Locale != ApprovedArtifacts().Locale {
		return invalid("locale")
	}
	a := in.Administrator
	if a.Title == "" || len(a.Title) > 200 || !utf8.ValidString(a.Title) || strings.IndexFunc(a.Title, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
		return invalid("title")
	}
	if !adminUsername.MatchString(a.Username) {
		return invalid("administrator username")
	}
	parsed, err := mail.ParseAddress(a.Email)
	if len(a.Email) > 254 || strings.ContainsAny(a.Email, "\r\n\x00") || err != nil || parsed.Address != a.Email || parsed.Name != "" {
		return invalid("administrator email")
	}
	return nil
}
