package dns

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestIntegrationTokenCopiedRedaction(t *testing.T) {
	const secret = "synthetic-copied-token-only"
	token, err := NewIntegrationToken([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	defer token.Destroy()
	var nilToken *IntegrationToken
	values := map[string]any{
		"pointer": token, "copy": *token,
		"nested":         struct{ Token IntegrationToken }{*token},
		"private-nested": struct{ token IntegrationToken }{*token},
		"slice":          []IntegrationToken{*token},
		"map-value":      map[string]IntegrationToken{"token": *token},
		"map-key":        map[*IntegrationToken]string{token: "token"},
		"nil":            nilToken, "zero": IntegrationToken{},
	}
	formats := []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%p", "%w", "%T", "%t", "%b", "%c", "%d", "%o", "%O", "%U", "%e", "%E", "%f", "%F", "%g", "%G", "%a", "%z", "%20.8s", "%#020x", "%+30.5v", "%[2]v", "%*s", "%.*s"}
	forbidden := []string{secret, hex.EncodeToString([]byte(secret)), strings.ToUpper(hex.EncodeToString([]byte(secret))), fmt.Sprint([]byte(secret)), strings.Trim(fmt.Sprint([]byte(secret)), "[]"), fmt.Sprintf("%#v", []byte(secret))}
	check := func(out, name string) {
		t.Helper()
		for _, text := range forbidden {
			if strings.Contains(out, text) {
				t.Errorf("credential exposed by %s", name)
				break
			}
		}
	}
	for name, value := range values {
		for _, format := range formats {
			check(fmt.Sprintf(format, value), name+"/"+format)
			check(fmt.Errorf(format, value).Error(), "error/"+name+"/"+format)
		}
		if name == "private-nested" || name == "map-key" {
			continue
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Errorf("JSON %s failed", name)
			continue
		}
		check(string(raw), "JSON/"+name)
		if (name == "copy" || name == "pointer") && string(raw) != `"[REDACTED]"` {
			t.Errorf("JSON %s did not redact", name)
		}
	}
}

func TestIntegrationTokenCopyDestruction(t *testing.T) {
	input := []byte("synthetic-lifetime-token-only")
	token, err := NewIntegrationToken(input)
	if err != nil {
		t.Fatal(err)
	}
	defer token.Destroy()
	copyToken := *token
	execution := token.ExecutionBytes()
	defer clear(execution)
	input[0] = 'X'
	if !bytes.Equal(copyToken.ExecutionBytes(), execution) {
		t.Fatal("constructor or value copy changed execution bytes")
	}
	execution[0] = 'Y'
	if bytes.Equal(token.ExecutionBytes(), execution) {
		t.Fatal("execution bytes alias token storage")
	}
	token.Destroy()
	if len(token.ExecutionBytes()) != 0 {
		t.Fatal("destroyed owner retained bytes")
	}
	copiedBytes := copyToken.ExecutionBytes()
	defer clear(copiedBytes)
	if len(copiedBytes) != len(input) || !bytes.Equal(copiedBytes, make([]byte, len(input))) {
		t.Fatal("destroy did not clear shared copied-token buffer")
	}
	copyToken.Destroy()
	token.Destroy()
	var zero IntegrationToken
	zero.Destroy()
	if len(zero.ExecutionBytes()) != 0 {
		t.Fatal("zero token retained bytes")
	}
}
