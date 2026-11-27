package jobs

import (
	"reflect"
	"testing"

	"novapanel/internal/store"
)

func TestNextCompositeChild(t *testing.T) {
	if got := compositeChildKinds(true, true); !reflect.DeepEqual(got, []string{"maintenance_on", "php_change", "domain_tls", "maintenance_off"}) {
		t.Fatal(got)
	}
	if got := compositeChildKinds(true, false); !reflect.DeepEqual(got, []string{"php_change", "maintenance_off"}) {
		t.Fatal(got)
	}
	children := []store.OperationChild{{Ordinal: 0, Kind: "maintenance_on", Status: "committed"}, {Ordinal: 1, Kind: "php_change", Status: "pending"}, {Ordinal: 2, Kind: "domain_tls", Status: "pending"}, {Ordinal: 3, Kind: "maintenance_off", Status: "pending"}}
	if next, err := nextCompositeChild(children); err != nil || next != 1 {
		t.Fatalf("resume: %d %v", next, err)
	}
	children[1].Status = "failed"
	if next, err := nextCompositeChild(children); err != nil || next != 3 {
		t.Fatalf("failure: %d %v", next, err)
	}
	children[3].Status = "committed"
	if next, err := nextCompositeChild(children); err != nil || next != -1 {
		t.Fatalf("terminal: %d %v", next, err)
	}
}
