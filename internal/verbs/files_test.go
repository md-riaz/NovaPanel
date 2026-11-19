package verbs

import (
	"novapanel/internal/rpc"
	"testing"
)

func TestFilesRegistryAndValidation(t *testing.T) {
	for _, name := range []string{"files.list", "files.read"} {
		v := rpc.Registry[name]
		if v.MinRole != rpc.Admin || v.Scope != "sites:read" || !v.ReadOnly || v.MFA || v.Slow {
			t.Fatalf("%s: %+v", name, v)
		}
	}
	id := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	for _, p := range []string{"../", "a/../b", "/etc/passwd", "a\x00b"} {
		if (&filesListArgs{SiteID: id, Path: p}).Validate() == nil {
			t.Errorf("accepted %q", p)
		}
	}
	if (&filesReadArgs{SiteID: id, Offset: -1}).Validate() == nil || (&filesReadArgs{SiteID: id, Length: -1}).Validate() == nil {
		t.Fatal("accepted negative offset or length")
	}
}
