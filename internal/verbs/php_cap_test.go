package verbs

import (
	"novapanel/internal/store"
	"testing"
)

func TestCommittedPHPCapFailsClosed(t *testing.T) {
	for _, v := range []int{0, 1, 33} {
		if _, err := committedPHPCap(store.PHPLimits{MaxChildren: &v}); err == nil {
			t.Fatalf("accepted invalid cap %d", v)
		}
	}
	if _, err := committedPHPCap(store.PHPLimits{}); err == nil {
		t.Fatal("accepted legacy missing cap")
	}
	v := 7
	if got, err := committedPHPCap(store.PHPLimits{MaxChildren: &v}); err != nil || got != v {
		t.Fatalf("got %d: %v", got, err)
	}
}
