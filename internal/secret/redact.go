package secret

import (
	"encoding/json"
	"regexp"
	"strings"
)

var tokenShape = regexp.MustCompile(`(?i)(?:npat_|nps_)[a-z0-9_-]+|Bearer\s+[^\s"',;]+|-----BEGIN [^-]*PRIVATE KEY-----[\s\S]*?(?:-----END [^-]*PRIVATE KEY-----|$)`)

func sensitive(k string) bool {
	k = strings.ToLower(k)
	for _, s := range []string{"password", "passwd", "secret", "token", "claim", "credential", "authorization", "cookie", "private_key", "key_pem", "pw_phc", "nonce", "ciphertext", "totp", "backup_codes"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return k == "key"
}
func text(s string) string { return tokenShape.ReplaceAllString(s, "[REDACTED]") }

// Redact returns a deep JSON copy so it also handles typed slices/maps and
// structs. Unencodable input fails closed instead of preserving unknown values.
func Redact(m map[string]any) map[string]any {
	b, e := json.Marshal(m)
	if e != nil {
		return map[string]any{"redacted": "[UNENCODABLE]"}
	}
	var out map[string]any
	if e = json.Unmarshal(b, &out); e != nil {
		return map[string]any{"redacted": "[UNENCODABLE]"}
	}
	walk(out)
	return out
}
func walk(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if sensitive(k) {
				x[k] = "[REDACTED]"
				continue
			}
			if s, ok := v.(string); ok {
				x[k] = text(s)
			} else {
				walk(v)
			}
		}
	case []any:
		for i, v := range x {
			if s, ok := v.(string); ok {
				x[i] = text(s)
			} else {
				walk(v)
			}
		}
	}
}

// RedactText removes known secret shapes from free-form labels too.
func RedactText(s string) string { return text(s) }
