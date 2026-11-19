package verbs

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"novapanel/internal/rpc"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestSSHKeyRegistry(t *testing.T) {
	for _, tc := range []struct {
		name          string
		role          rpc.Role
		scope         string
		mfa, readOnly bool
	}{
		{"sshkey.add", rpc.Admin, "ssh:write", true, false}, {"sshkey.remove", rpc.Admin, "ssh:write", true, false}, {"sshkey.list", rpc.ReadOnly, "sites:read", false, true},
	} {
		v, ok := rpc.Registry[tc.name]
		if !ok || v.MinRole != tc.role || v.Scope != tc.scope || v.MFA != tc.mfa || v.ReadOnly != tc.readOnly || v.Slow || v.Exec != nil || v.NewJobArgs != nil {
			t.Errorf("invalid registration %s: %+v", tc.name, v)
		}
	}
}

func TestSSHKeyCommentLimit(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))) + " "
	args := sshkeyAddArgs{SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", PublicKey: base + strings.Repeat("<>&", 85)}
	if err = args.Validate(); err != nil {
		t.Fatalf("255-byte comment: %v", err)
	}
	args.PublicKey += "x"
	var coded *rpc.CodedError
	if err = args.Validate(); !errors.As(err, &coded) || coded.Code != "invalid_argument" {
		t.Fatalf("overlong comment: %v", err)
	}
}

func TestSSHKeyID(t *testing.T) {
	for _, s := range []string{"", "01ARZ3NDEKTSV4RRFFQ69G5FAV.bad", "bad.SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if err := (&sshkeyRemoveArgs{KeyID: s}).Validate(); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}
