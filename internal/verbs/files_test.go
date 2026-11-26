package verbs

import (
	"encoding/base64"
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

func TestFilesWriteRegistryAndValidation(t *testing.T) {
	for _, name := range []string{"files.write", "files.mkdir", "files.move", "files.delete", "files.chmod"} {
		v, ok := rpc.Registry[name]
		if !ok || v.MinRole != rpc.Admin || v.Scope != "sites:write" || v.ReadOnly || v.MFA || v.Slow || v.Run == nil {
			t.Fatalf("%s: %+v", name, v)
		}
	}
	id := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	for _, p := range []string{"../bad", "/bad", "bad\x00name", "bad\\name"} {
		for _, v := range []interface{ Validate() error }{&filesWriteArgs{SiteID: id, Path: p}, &filesMkdirArgs{SiteID: id, Path: p}, &filesMoveArgs{SiteID: id, From: p, To: "ok"}, &filesMoveArgs{SiteID: id, From: "ok", To: p}, &filesDeleteArgs{SiteID: id, Path: p}, &filesChmodArgs{SiteID: id, Path: p, Mode: 0644}} {
			if v.Validate() == nil {
				t.Fatalf("accepted %q by %T", p, v)
			}
		}
	}
	if (&filesChmodArgs{SiteID: id, Mode: 0777}).Validate() == nil {
		t.Fatal("accepted mode")
	}
	if (&filesWriteArgs{SiteID: id, ContentB64: base64.StdEncoding.EncodeToString(make([]byte, 768<<10))}).Validate() != nil {
		t.Fatal("rejected maximum write")
	}
	if (&filesWriteArgs{SiteID: id, ContentB64: base64.StdEncoding.EncodeToString(make([]byte, (768<<10)+1))}).Validate() == nil {
		t.Fatal("accepted oversized write")
	}
}
