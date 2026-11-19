package sshkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func testKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k))) + " test-comment"
}

func TestLifecycle(t *testing.T) {
	home := t.TempDir()
	site := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	key := testKey(t)
	if _, _, err := Parse("-----BEGIN PRIVATE KEY-----"); err == nil {
		t.Fatal("accepted private key")
	}
	if _, _, err := Parse("bad"); err == nil {
		t.Fatal("accepted malformed key")
	}
	if _, _, err := Parse(key + "\n" + key); err == nil {
		t.Fatal("accepted multiple keys")
	}
	if _, _, err := Parse(key + " " + key); err == nil {
		t.Fatal("accepted multiple keys on one line")
	}
	add := Request{Home: home, SiteID: site, Operation: "add", Value: "command=\"bad\" " + key}
	first, err := Apply(add)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".ssh", "authorized_keys")
	original, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Apply(add)
	if err != nil || first.KeyID != second.KeyID {
		t.Fatalf("idempotence: %+v %v", second, err)
	}
	current, err := os.Stat(path)
	if err != nil || !os.SameFile(original, current) {
		t.Fatal("duplicate add replaced file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "command=") || strings.Count(string(data), "ssh-ed25519") != 1 {
		t.Fatalf("unexpected content: %s", data)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("mode: %v %v", info, err)
	}
	list, err := Apply(Request{Home: home, SiteID: site, Operation: "list"})
	if err != nil || len(list.Keys) != 1 || list.Keys[0].ID != first.KeyID || list.Keys[0].Type != "ssh-ed25519" || list.Keys[0].Comment != "test-comment" || first.KeyID != site+"."+list.Keys[0].Fingerprint {
		t.Fatalf("list: %+v %v", list, err)
	}
	secondKey := testKey(t)
	_, err = Apply(Request{Home: home, SiteID: site, Operation: "add", Value: secondKey})
	if err != nil {
		t.Fatal(err)
	}
	_, fp, _ := strings.Cut(first.KeyID, ".")
	_, err = Apply(Request{Home: home, SiteID: site, Operation: "remove", Value: fp})
	if err != nil {
		t.Fatal(err)
	}
	list, err = Apply(Request{Home: home, SiteID: site, Operation: "list"})
	if err != nil || len(list.Keys) != 1 || list.Keys[0].ID == first.KeyID {
		t.Fatalf("remove: %+v %v", list, err)
	}
	res, err := Apply(Request{Home: home, SiteID: site, Operation: "remove", Value: fp})
	if err != nil || !res.NotFound {
		t.Fatalf("missing: %+v %v", res, err)
	}
}

func TestRefusesSymlinkDirectory(t *testing.T) {
	home := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(home, ".ssh")); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(Request{Home: home, Operation: "add", Value: testKey(t)}); err == nil {
		t.Fatal("accepted symlink")
	}
}
