package firewall

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

const custodyID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

func custodyInput() SnapshotInput {
	in := SnapshotInput{PriorEnabled: true, Rules: map[string][]byte{}, IPTablesSave: []byte("v4 capture"), IP6TablesSave: []byte("v6 capture"), NumberedStatus: []byte("numbered capture")}
	for _, n := range []string{"after.rules", "after6.rules", "before.rules", "before6.rules", "user.rules", "user6.rules"} {
		in.Rules[n] = []byte("scratch " + n)
	}
	return in
}
func custodyTempDir(t *testing.T) string {
	t.Helper()
	// Do not weaken production's writable-ancestor refusal for /tmp-based CI.
	// Keep volatile fixtures outside the source tree. Architecture checks walk
	// the tree concurrently and must never observe directories being removed.
	// TMPDIR is supplied by the scratch/root test launcher, not provisioned here.
	dir, e := os.MkdirTemp(os.TempDir(), ".snapshot-scratch-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := os.RemoveAll(dir); e != nil {
			t.Error(e)
		}
	})
	return dir
}
func custodyFixture(t *testing.T) (*SnapshotCustody, string) {
	t.Helper()
	root := filepath.Join(custodyTempDir(t), "custody")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	c, err := OpenSnapshotCustody(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, root
}
func custodyRecord(digest string) JournalRecord {
	return JournalRecord{Version: 1, ID: custodyID, PriorEnabled: true, SnapshotSHA256: digest}
}
func TestSnapshotCustodyRoundTrip(t *testing.T) {
	c, root := custodyFixture(t)
	ctx := context.Background()
	in := custodyInput()
	digest, err := c.Put(ctx, custodyID, in)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(root, custodyID, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if digest != fmt.Sprintf("%x", sha256.Sum256(manifest)) {
		t.Fatal("digest does not cover exact manifest")
	}
	other, err := OpenSnapshotCustody(root)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	s, err := other.Verify(ctx, custodyRecord(digest))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	want := map[string][]byte{"enabled": []byte("true\n"), "iptables-save": in.IPTablesSave, "ip6tables-save": in.IP6TablesSave, "ufw-status-numbered": in.NumberedStatus}
	for n, b := range in.Rules {
		want["ufw/"+n] = b
	}
	if len(s.files) != len(want) {
		t.Fatal("incomplete descriptors")
	}
	for n, b := range want {
		got, e := io.ReadAll(s.files[n])
		if e != nil || string(got) != string(b) {
			t.Fatalf("artifact mismatch %s", n)
		}
		st, e := s.files[n].Stat()
		if e != nil || st.Mode().Perm() != 0600 {
			t.Fatal("artifact mode")
		}
	}
	again, err := other.Put(ctx, custodyID, in)
	if err != nil || again != digest {
		t.Fatal("identical replay", err)
	}
}
