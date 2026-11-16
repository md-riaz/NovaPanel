package verbs

import (
	"strings"
	"testing"
)

func TestSiteCreateWordPressArguments(t *testing.T) {
	valid := siteWordPressArgs{Title: "Example", AdminUsername: "admin", AdminEmail: "owner@example.test"}
	for _, tc := range []struct {
		name, kind string
		wp         *siteWordPressArgs
		want       string
	}{
		{"missing", "wordpress", nil, "WordPress settings"},
		{"static", "static", &valid, "WordPress settings"},
		{"invalid admin", "wordpress", &siteWordPressArgs{Title: "Example", AdminUsername: "--bad", AdminEmail: "owner@example.test"}, "administrator username"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := (&siteCreateArgs{Type: tc.kind, PrimaryDomain: "example.test", WordPress: tc.wp}).Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v", err)
			}
		})
	}
}
