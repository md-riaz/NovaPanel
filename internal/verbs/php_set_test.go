package verbs

import (
	"encoding/json"
	php "novapanel/internal/render/php"
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
		err := json.Unmarshal([]byte(tc.raw), &args)
		if err == nil {
			_, err = php.MergeLimits(nil, args.request.Limits)
		}
		if (err == nil) != tc.valid {
			t.Fatalf("validation %s", tc.raw)
		}
	}
}
