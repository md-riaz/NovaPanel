package verbs

import (
	"encoding/json"
	"testing"
)

func TestPHPSetValidation(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{`{"site_id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","version":"8.3"}`, true},
		{`{"site_id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","version":"8.2"}`, false},
		{`{"site_id":"01ARZ3NDEKTSV4RRFFQ69G5FAV"}`, false},
		{`{"site_id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","version":"8.3","limits":{"memory_mb":0}}`, false},
	} {
		var args phpSetArgs
		if (json.Unmarshal([]byte(tc.raw), &args) == nil) != tc.valid {
			t.Fatalf("validation %s", tc.raw)
		}
	}
}
