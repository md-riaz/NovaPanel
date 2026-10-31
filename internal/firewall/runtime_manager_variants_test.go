package firewall

import (
	"testing"
)

func TestManagerAcceptsWellTypedSystemdUnrelatedVariants(t *testing.T) {
	for _, tc := range []struct{ sig, data string }{{"x", "-1"}, {"u", "1"}, {"i", "-1"}, {"y", "255"}, {"n", "-32768"}, {"q", "65535"}, {"d", "1.25"}, {"a(ayu)", `[[[1,2],3]]`}, {"v", `{"type":"s","data":"value"}`}, {"a{ss}", `{"a":"b"}`}} {
		t.Run(tc.sig, func(t *testing.T) {
			v, e := decodeManager([]byte(tc.data))
			if e != nil || !typedValue(tc.sig, v) {
				t.Fatal("well typed unrelated variant rejected", tc, e)
			}
		})
	}
}
