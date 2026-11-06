package main

import (
	"bytes"
	"testing"
)

func TestHelperCommandStaysGated(t *testing.T) {
	for _, args := range [][]string{nil, {"apply"}, {"prepare"}, {"prepare", "https://example.test/bundle"}, {"recover"}} {
		var out bytes.Buffer
		if run(args, &out) == 0 {
			t.Fatal("accepted unavailable helper operation", args)
		}
	}
}
