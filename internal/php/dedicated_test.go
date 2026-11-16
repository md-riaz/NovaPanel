package php

import (
	"strings"
	"testing"
)

func TestDedicatedMasterSinglePool(t *testing.T) {
	a, err := DedicatedMaster("siteone")
	if err != nil {
		t.Fatal(err)
	}
	b, err := DedicatedMaster("sitetwo")
	if err != nil {
		t.Fatal(err)
	}
	if string(a) == string(b) || strings.Count(string(a), "include = ") != 1 || !strings.Contains(string(a), "/siteone.conf\n") || strings.ContainsAny(string(a), "*?") {
		t.Fatal("master must use exactly one site pool")
	}
	for _, bad := range []string{"../other", "foo\ninclude=/tmp/evil", ""} {
		if _, err := DedicatedMaster(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
