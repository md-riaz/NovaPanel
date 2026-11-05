package database

import (
	"context"
	"io"
	"novapanel/internal/exec"
	"strings"
	"testing"
)

type recorder struct {
	args  []string
	input string
}

func (r *recorder) Run(_ context.Context, _ string, args []string, opts exec.RunOpts) ([]byte, []byte, int, error) {
	r.args = args
	b, _ := io.ReadAll(opts.Stdin)
	r.input = string(b)
	return nil, nil, 0, nil
}
func TestCreateRejectsSQLInjection(t *testing.T) {
	for _, tc := range [][3]string{{"d_x`;DROP", "u_test", "secret"}, {"d_test", "u_x'@'%'", "secret"}, {"d_test", "u_test", "x';DROP"}} {
		r := &recorder{}
		if err := Create(context.Background(), r, tc[0], tc[1], tc[2]); err == nil || r.args != nil {
			t.Fatalf("unsafe input accepted: %q", tc)
		}
	}
}

func TestRotateUsesSocketAndNoArgvSecret(t *testing.T) {
	r := &recorder{}
	if err := Rotate(context.Background(), r, "u_test", "abc123"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(r.args, " "), "abc123") || !strings.Contains(r.input, "ALTER USER 'u_test'@'127.0.0.1'") {
		t.Fatal("invalid rotation command")
	}
	if err := Rotate(context.Background(), &recorder{}, "u_test", "x';DROP"); err == nil {
		t.Fatal("accepted unsafe password")
	}
}

func TestCreateUserScopesPrivileges(t *testing.T) {
	r := &recorder{}
	if err := CreateUser(context.Background(), r, "d_test", "u_test", "abc123"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.input, "CREATE USER 'u_test'@'127.0.0.1'") || !strings.Contains(r.input, "`d\\_test`.*") || strings.Contains(strings.Join(r.args, " "), "abc123") {
		t.Fatalf("invalid create user SQL: %s", r.input)
	}
	if strings.Contains(r.input, " ON *.*") || strings.Contains(r.input, "GRANT ALL") || strings.Contains(r.input, "GRANT OPTION") {
		t.Fatal("overbroad privilege")
	}
	if err := CreateUser(context.Background(), &recorder{}, "d_test", "u_test", "x';DROP"); err == nil {
		t.Fatal("unsafe password")
	}
}

func TestDeleteUsesOnlyValidatedLoopbackUsers(t *testing.T) {
	r := &recorder{}
	if err := Delete(context.Background(), r, "d_test", []string{"u_test"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.input, "DROP USER IF EXISTS 'u_test'@'127.0.0.1'") || !strings.Contains(r.input, "DROP DATABASE IF EXISTS `d_test`") {
		t.Fatalf("SQL: %s", r.input)
	}
	bad := &recorder{}
	if err := Delete(context.Background(), bad, "d_test", []string{"u_test';DROP"}); err == nil || bad.args != nil {
		t.Fatal("unsafe delete")
	}
}

func TestCreateUsesSocketAndScopedGrant(t *testing.T) {
	r := &recorder{}
	if err := Create(context.Background(), r, "d_test", "u_test", "abc123"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(r.args, " "), "abc123") || !strings.Contains(strings.Join(r.args, " "), "--protocol=socket") {
		t.Fatalf("args: %v", r.args)
	}
	if !strings.Contains(r.input, "`d\\_test`.*") || !strings.Contains(r.input, "'u_test'@'127.0.0.1'") {
		t.Fatalf("SQL: %s", r.input)
	}
}
