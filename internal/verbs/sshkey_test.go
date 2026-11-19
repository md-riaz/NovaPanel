package verbs

import (
	"novapanel/internal/rpc"
	"testing"
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

func TestSSHKeyID(t *testing.T) {
	for _, s := range []string{"", "01ARZ3NDEKTSV4RRFFQ69G5FAV.bad", "bad.SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if err := (&sshkeyRemoveArgs{KeyID: s}).Validate(); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}
