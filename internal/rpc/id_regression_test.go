package rpc

import "testing"

func TestDecodeRequiresULID(t *testing.T) {
	for _, id := range []string{"", "1", "abc", "81ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FAI"} {
		if _, err := DecodeLine([]byte(`{"id":"` + id + `","verb":"ping","args":{}}`)); err == nil {
			t.Errorf("accepted invalid request ID %q", id)
		}
	}
}
