package verbs

import (
	"novapanel/internal/store"
	"testing"
)

func TestPHPSetValidation(t *testing.T) {
	id := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	n := 0
	for _, tc := range []struct {
		args  phpSetArgs
		valid bool
	}{
		{phpSetArgs{SiteID: id, Version: "8.3"}, true},
		{phpSetArgs{SiteID: id, Version: "8.2"}, false},
		{phpSetArgs{SiteID: id}, false},
		{phpSetArgs{SiteID: id, Version: "8.3", Limits: store.PHPLimits{MemoryMB: &n}}, false},
	} {
		if (tc.args.Validate() == nil) != tc.valid {
			t.Fatalf("validation %+v", tc.args)
		}
	}
}
