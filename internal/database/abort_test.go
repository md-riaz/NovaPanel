package database

import (
	"context"
	"strings"
	"testing"
)

func TestAbortMarksAllBeforeDroppingAndRetainsMarkers(t *testing.T) {
	r := &provenanceRunner{state: "created\t1\t01ARZ3NDEKTSV4RRFFQ69G5FAV"}
	err := AbortCreate(context.Background(), r, []AbortObject{{"schema", "d_test", "01ARZ3NDEKTSV4RRFFQ69G5FAV"}, {"user", "u_test", "01ARZ3NDEKTSV4RRFFQ69G5FAV"}})
	if err != nil {
		t.Fatal(err)
	}
	marks, firstDrop := 0, -1
	for i, s := range r.statements {
		if strings.Contains(s, "SET phase='deleting'") {
			marks++
		}
		if strings.HasPrefix(s, "DROP ") && firstDrop < 0 {
			firstDrop = i
			if marks != 2 {
				t.Fatal("drop preceded marking all owned objects")
			}
		}
		if strings.HasPrefix(s, "DELETE ") {
			t.Fatal("removed marker before terminal job")
		}
	}
	if marks != 2 || firstDrop < 0 {
		t.Fatalf("missing abort actions: %v", r.statements)
	}
}
