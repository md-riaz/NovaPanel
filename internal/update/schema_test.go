package update

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestVerifyRejectsNullOptionalMetadata(t *testing.T) {
	old := releasePublicKeyBase64
	t.Cleanup(func() { releasePublicKeyBase64 = old })
	for _, field := range []string{"security_fix", "notes"} {
		t.Run(field, func(t *testing.T) {
			body := strings.Replace(goodManifest, `"security_fix":true`, `"`+field+`":null`, 1)
			pub, sig := signed(t, body)
			releasePublicKeyBase64 = base64.StdEncoding.EncodeToString(pub)
			_, _, err := Verify([]byte(body), sig, "dev")
			code(t, err, "invalid_argument")
		})
	}
}
