package configcustody

import "strings"

// ComposeConfig is the config-create equivalent for an agent-owned installation.
// It accepts data only, never a template, PHP fragment, or site-controlled bytes.
// The caller supplies the operation's already sealed, locally generated salts.
func ComposeConfig(database, username, password string, salts [8]string) ([]byte, error) {
	validName := func(s, prefix string) bool {
		if !strings.HasPrefix(s, prefix) || len(s) > 64 || len(s) <= len(prefix) {
			return false
		}
		for _, c := range s[len(prefix):] {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
				return false
			}
		}
		return true
	}
	if !validName(database, "d_") || !validName(username, "u_") || password == "" || len(password) > 4096 || strings.ContainsRune(password, 0) {
		return nil, errConfigCustody
	}
	for _, s := range salts {
		if s == "" || len(s) > 4096 || strings.ContainsRune(s, 0) {
			return nil, errConfigCustody
		}
	}
	quote := func(s string) string {
		return "'" + strings.ReplaceAll(strings.ReplaceAll(s, "\\", "\\\\"), "'", "\\'") + "'"
	}
	var out strings.Builder
	out.WriteString("<?php\n")
	define := func(k, v string) { out.WriteString("define(" + quote(k) + ", " + quote(v) + ");\n") }
	define("DB_NAME", database)
	define("DB_USER", username)
	define("DB_PASSWORD", password)
	define("DB_HOST", "127.0.0.1")
	define("DB_CHARSET", "utf8mb4")
	define("DB_COLLATE", "")
	for i, name := range []string{"AUTH_KEY", "SECURE_AUTH_KEY", "LOGGED_IN_KEY", "NONCE_KEY", "AUTH_SALT", "SECURE_AUTH_SALT", "LOGGED_IN_SALT", "NONCE_SALT"} {
		define(name, salts[i])
	}
	out.WriteString("$table_prefix = 'wp_';\nif (!defined('ABSPATH')) { define('ABSPATH', __DIR__ . '/'); }\nrequire_once ABSPATH . 'wp-settings.php';\n")
	return []byte(out.String()), nil
}
