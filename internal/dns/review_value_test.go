package dns

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

func TestReviewIntegrationTokenValueFormatting(t *testing.T) {
	const secret = "review-synthetic-secret-only"
	token, err := NewIntegrationToken([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	defer token.Destroy()
	for name, value := range map[string]any{"pointer": token, "value": *token, "nested-value": struct{ Token IntegrationToken }{*token}} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%x", "%X"} {
			out := fmt.Sprintf(format, value)
			if strings.Contains(out, secret) || strings.Contains(out, hex.EncodeToString([]byte(secret))) || strings.Contains(out, strings.ToUpper(hex.EncodeToString([]byte(secret)))) || strings.Contains(out, fmt.Sprint([]byte(secret))) {
				t.Errorf("credential exposed by %s with %s", name, format)
			}
		}
	}
}
