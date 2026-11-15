package verbs

import (
	"novapanel/internal/store"
	"testing"
)

func TestPHPSetValidation(t *testing.T) {
	id := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	n := 0
	for _, tc := range []struct {
		args  legacyPHPSetArgs
		valid bool
	}{
		{legacyPHPSetArgs{SiteID: id, Version: "8.3"}, true},
		{legacyPHPSetArgs{SiteID: id, Version: "8.2"}, false},
		{legacyPHPSetArgs{SiteID: id}, false},
		{legacyPHPSetArgs{SiteID: id, Version: "8.3", Limits: store.PHPLimits{MemoryMB: &n}}, false},
	} {
		if (tc.args.Validate() == nil) != tc.valid {
			t.Fatalf("validation %+v", tc.args)
		}
	}
}
