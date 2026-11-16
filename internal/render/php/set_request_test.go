package php

import "testing"

func TestDecodeSetRequest(t *testing.T) {
	valid := `{"site_id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","version":"8.3","limits":{"memory_mb":512}}`
	r, err := DecodeSetRequest([]byte(valid))
	if err != nil || r.Version != "8.3" || r.Limits.MemoryMB == nil || *r.Limits.MemoryMB != 512 || r.Limits.UploadMB != nil {
		t.Fatalf("request=%+v err=%v", r, err)
	}
	for _, input := range []string{
		`{"site_id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","version":"8.2"}`,
		`{"site_id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","version":"8.3","version":"8.3"}`,
		`{"site_id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","version":"8.3","limits":{"memory_mb":128,"memory_mb":256}}`,
		`{"site_id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","version":"8.3","limits":{"memory_mb":null}}`,
		`{"site_id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","version":"8.3","limits":null}`,
		`{"site_id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","version":"8.3","limits":{"hardened":true}}`,
		`{"site_id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","version":"8.3"} {}`,
		`{"site_id":"bad","version":"8.3"}`,
	} {
		if _, err := DecodeSetRequest([]byte(input)); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
}
