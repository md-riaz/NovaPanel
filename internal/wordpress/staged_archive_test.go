package wordpress

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"testing"
)

// Private fixture pins exercise parser policy, not a production digest bypass.
func TestStagedArchiveStrictTree(t *testing.T) {
	type entry struct {
		name string
		mode os.FileMode
		body string
	}
	index := entry{"wordpress/index.php", 0644, "<?php // fixture"}
	version := entry{"wordpress/wp-includes/version.php", 0644, "<?php\n$wp_version = '7.1.4';\n"}
	cases := []struct {
		name    string
		entries []entry
		valid   bool
	}{
		{"implicit_parents", nil, true},
		{"explicit_root_and_parent", []entry{{"wordpress/", os.ModeDir | 0755, ""}, {"wordpress/wp-includes/", os.ModeDir | 0755, ""}}, true},
		{"duplicate_root", []entry{{"wordpress/", os.ModeDir | 0755, ""}, {"wordpress/", os.ModeDir | 0755, ""}}, false},
		{"duplicate_directory", []entry{{"wordpress/empty/", os.ModeDir | 0755, ""}, {"wordpress/empty/", os.ModeDir | 0755, ""}}, false},
		{"directory_below_file", []entry{{"wordpress/index.php/sub/", os.ModeDir | 0755, ""}}, false},
		{"deep_directory_below_file", []entry{{"wordpress/index.php/a/b/", os.ModeDir | 0755, ""}}, false},
		{"directory_equals_file", []entry{{"wordpress/index.php/", os.ModeDir | 0755, ""}}, false},
		{"file_below_file", []entry{{"wordpress/index.php/child", 0644, "child"}}, false},
		{"duplicate_file", []entry{index}, false},
		{"dotdot_directory", []entry{{"wordpress/../", os.ModeDir | 0755, ""}}, false},
		{"dotdot_file", []entry{{"wordpress/..", 0644, "parent"}}, false},
		{"dot_directory", []entry{{"wordpress/./", os.ModeDir | 0755, ""}}, false},
		{"dot_component", []entry{{"wordpress/a/./b/", os.ModeDir | 0755, ""}}, false},
		{"dotdot_component", []entry{{"wordpress/a/../b/", os.ModeDir | 0755, ""}}, false},
		{"double_slash", []entry{{"wordpress/a//b/", os.ModeDir | 0755, ""}}, false},
		{"directory_without_slash", []entry{{"wordpress/empty", os.ModeDir | 0755, ""}}, false},
	}
	for _, typ := range []struct {
		name string
		mode os.FileMode
	}{
		{"symlink", os.ModeSymlink}, {"fifo", os.ModeNamedPipe}, {"socket", os.ModeSocket},
		{"device", os.ModeDevice}, {"char_device", os.ModeDevice | os.ModeCharDevice},
		// Slash-symlink headers below decode to mixed ModeDir|ModeSymlink.
	} {
		for _, name := range []string{"wordpress/", "wordpress/evil/", "wordpress/evil"} {
			cases = append(cases, struct {
				name    string
				entries []entry
				valid   bool
			}{typ.name + "_" + name, []entry{{name, typ.mode | 0755, ""}}, false})
		}
	}
	for _, tc := range cases {
		for _, extrasFirst := range []bool{true, false} {
			order := "extras_last"
			if extrasFirst {
				order = "extras_first"
			}
			t.Run(tc.name+"/"+order, func(t *testing.T) {
				p, m := stageFixture(t)
				entries := append([]entry{index, version}, tc.entries...)
				if extrasFirst {
					entries = append(append([]entry{}, tc.entries...), index, version)
				}
				var b bytes.Buffer
				z := zip.NewWriter(&b)
				sums := map[string]string{}
				for _, e := range entries {
					h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
					h.SetMode(e.mode)
					w, err := z.CreateHeader(h)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = io.WriteString(w, e.body); err != nil {
						t.Fatal(err)
					}
					if e.mode.IsRegular() {
						sum := md5.Sum([]byte(e.body))
						sums[e.name[len("wordpress/"):]] = hex.EncodeToString(sum[:])
					}
				}
				if err := z.Close(); err != nil {
					t.Fatal(err)
				}
				metadata, err := json.Marshal(map[string]any{"checksums": sums})
				if err != nil {
					t.Fatal(err)
				}
				for _, f := range []struct {
					path string
					raw  []byte
				}{{p.Archive, b.Bytes()}, {p.Checksums, metadata}} {
					if err := os.WriteFile(f.path, f.raw, 0644); err != nil {
						t.Fatal(err)
					}
				}
				hash := sha256.Sum256(b.Bytes())
				m.WordPress.SHA256 = hex.EncodeToString(hash[:])
				hash = sha256.Sum256(metadata)
				m.ChecksumsSHA256 = hex.EncodeToString(hash[:])
				m.ChecksumEntries = len(sums)
				r := &stagedRunner{t: t, status: validStatus(m)}
				got, err := verifyStaged(context.Background(), p, r, m, uint32(os.Getuid()), true)
				if tc.valid {
					if err != nil || r.calls != 1 {
						t.Fatalf("valid tree refused: %v calls=%d", err, r.calls)
					}
				} else if err == nil || got != (ArtifactEvidence{}) || r.calls != 0 {
					t.Fatalf("unsafe tree accepted: %v calls=%d evidence=%+v", err, r.calls, got)
				}
			})
		}
	}
}
