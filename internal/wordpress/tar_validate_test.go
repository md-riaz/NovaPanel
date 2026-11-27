package wordpress

import (
	"archive/tar"
	"strings"
	"testing"
)

func TestValidateTarEntry(t *testing.T) {
	long := "wordpress/wp-includes/" + strings.Repeat("long-component/", 10) + "entry.php"
	if name, err := validateTarEntry(&tar.Header{Name: long, Typeflag: tar.TypeReg, Size: 12}); err != nil || name != strings.TrimPrefix(long, "wordpress/") {
		t.Fatalf("long path refused: %q %v", name, err)
	}
	for _, h := range []*tar.Header{
		{Name: "wordpress/../../etc/passwd", Typeflag: tar.TypeReg},
		{Name: "wordpress/a/../b", Typeflag: tar.TypeReg},
		{Name: "wordpress/link", Typeflag: tar.TypeSymlink, Linkname: "../../etc"},
		{Name: "wordpress/file", Typeflag: tar.TypeReg, Size: 64<<20 + 1},
	} {
		if _, err := validateTarEntry(h); err == nil {
			t.Fatalf("unsafe entry accepted: %#v", h)
		}
	}
}
