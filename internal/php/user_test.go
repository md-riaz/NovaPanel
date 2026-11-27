package php

import (
	"context"
	"errors"
	"fmt"
	runner "novapanel/internal/exec"
	"novapanel/internal/rpcerr"
	"strings"
	"testing"
)

const testID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

type fake struct {
	gecos   string
	missing bool
	calls   []string
}

type reservedFake struct {
	group        string
	uidTaken     bool
	webCollision bool
	calls        []string
}

func (f *reservedFake) Run(_ context.Context, p string, a []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	f.calls = append(f.calls, p+" "+strings.Join(a, " "))
	if p != "/usr/bin/getent" {
		return nil, nil, 0, nil
	}
	if a[0] == "passwd" && a[1] == "www-data" {
		if f.webCollision {
			return []byte("www-data:x:33:2000000::/var/www:/usr/sbin/nologin"), nil, 0, nil
		}
		return []byte("www-data:x:33:33::/var/www:/usr/sbin/nologin"), nil, 0, nil
	}
	if a[0] == "group" && a[1] == "www-data" {
		return []byte("www-data:x:33:"), nil, 0, nil
	}
	if a[0] == "passwd" && a[1] == "2000000" && f.uidTaken {
		return []byte("foreign:x:2000000:2000000::/:/bin/false"), nil, 0, nil
	}
	if a[0] == "group" && a[1] == "2000000" && f.group != "" {
		return []byte(f.group), nil, 0, nil
	}
	if a[0] == "gshadow" && f.group == "demo:x:2000000:" {
		return []byte("demo:!" + marker + testID + "::"), nil, 0, nil
	}
	return nil, nil, 2, errors.New("missing")
}

func TestEnsureReservedUser(t *testing.T) {
	collision := &reservedFake{webCollision: true}
	if err := EnsureReservedUser(context.Background(), collision, "/srv/sites", "demo", testID, 2000000); err == nil || strings.Contains(strings.Join(collision.calls, "\n"), "groupadd") {
		t.Fatal("web identity collision not rejected before creation")
	}
	for _, tc := range []struct {
		name, group string
		taken       bool
		wantError   bool
		groupadd    bool
	}{
		{"fresh", "", false, false, true},
		{"replay group", "demo:x:2000000:", false, false, false},
		{"foreign group", "other:x:2000000:", false, true, false},
		{"foreign uid", "", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &reservedFake{group: tc.group, uidTaken: tc.taken}
			err := EnsureReservedUser(context.Background(), f, "/srv/sites", "demo", testID, 2000000)
			calls := strings.Join(f.calls, "\n")
			if (err != nil) != tc.wantError || strings.Contains(calls, "groupadd") != tc.groupadd {
				t.Fatalf("calls=%s err=%v", calls, err)
			}
			if !tc.wantError && !strings.Contains(calls, "useradd -u 2000000 -g 2000000") {
				t.Fatal(calls)
			}
			if tc.wantError && strings.Contains(calls, "useradd") {
				t.Fatal(calls)
			}
		})
	}
}

func (f *fake) Run(_ context.Context, p string, a []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	f.calls = append(f.calls, p+" "+strings.Join(a, " "))
	if p == "/usr/bin/getent" {
		if f.missing {
			return nil, nil, 2, fmt.Errorf("not found")
		}
		return []byte("demo:x:1001:1001:" + f.gecos + ":/srv/sites/demo:/usr/sbin/nologin\n"), nil, 0, nil
	}
	return nil, nil, 0, nil
}
func TestOwnership(t *testing.T) {
	ctx := context.Background()
	for _, gc := range []string{"", marker + "01ARZ3NDEKTSV4RRFFQ69G5FAW"} {
		f := &fake{gecos: gc}
		if EnsureUser(ctx, f, "/srv/sites", "demo", testID) == nil || DeleteUser(ctx, f, "/srv/sites", "demo", testID) == nil {
			t.Fatal("foreign account accepted")
		}
		if len(f.calls) != 2 {
			t.Fatal(f.calls)
		}
	}
	f := &fake{gecos: marker + testID}
	if err := DeleteUser(ctx, f, "/srv/sites", "demo", testID); err != nil {
		t.Fatal(err)
	}
	f = &fake{gecos: marker + testID}
	if DeleteOrphan(ctx, f, "/srv/sites", "demo", testID, true) == nil || len(f.calls) != 0 {
		t.Fatal("live orphan deleted")
	}
	f = &fake{missing: true}
	if err := EnsureUser(ctx, f, "/srv/sites", "demo", testID); err != nil || !strings.Contains(f.calls[1], "-c "+marker+testID) {
		t.Fatal(f.calls, err)
	}
	f = &fake{}
	if err := MigrateUser(ctx, f, "/srv/sites", "demo", testID); err != nil || !strings.Contains(f.calls[1], "usermod") {
		t.Fatal(f.calls, err)
	}
}
func TestOrphanWithoutUIDFileProofIsNotDeleted(t *testing.T) {
	f := &fake{gecos: marker + testID}
	if err := DeleteOrphan(context.Background(), f, "/srv/sites", "demo", testID, false); err == nil {
		t.Fatal("orphan deleted without proof that UID-owned files are gone")
	}
	for _, call := range f.calls {
		if strings.Contains(call, "userdel") {
			t.Fatalf("unsafe user deletion: %v", f.calls)
		}
	}
}

func TestRetainUserOwnershipAndReplay(t *testing.T) {
	ctx := context.Background()
	for _, gc := range []string{"", marker + "01ARZ3NDEKTSV4RRFFQ69G5FAW", retainedMarker + "01ARZ3NDEKTSV4RRFFQ69G5FAW"} {
		f := &fake{gecos: gc}
		if RetainUser(ctx, f, "/srv/sites", "demo", testID) == nil || len(f.calls) != 1 {
			t.Fatal("foreign account changed", f.calls)
		}
	}
	f := &fake{gecos: marker + testID}
	if err := RetainUser(ctx, f, "/srv/sites", "demo", testID); err != nil || len(f.calls) != 2 || !strings.Contains(f.calls[1], "-c "+retainedMarker+testID) {
		t.Fatal(f.calls, err)
	}
	f = &fake{gecos: retainedMarker + testID}
	if err := RetainUser(ctx, f, "/srv/sites", "demo", testID); err != nil || len(f.calls) != 1 {
		t.Fatal(f.calls, err)
	}
	if EnsureUser(ctx, f, "/srv/sites", "demo", testID) == nil || DeleteOrphan(ctx, f, "/srv/sites", "demo", testID, false) == nil {
		t.Fatal("retained account adopted or deleted")
	}
	f = &fake{missing: true}
	if RetainUser(ctx, f, "/srv/sites", "demo", testID) == nil {
		t.Fatal("missing account accepted")
	}
}

func TestVerifyUserForDelete(t *testing.T) {
	for _, tc := range []struct {
		name, gecos, code string
		missing           bool
	}{
		{name: "active", gecos: marker + testID},
		{name: "retained", gecos: retainedMarker + testID},
		{name: "foreign active", gecos: marker + "01ARZ3NDEKTSV4RRFFQ69G5FAW", code: "conflict"},
		{name: "foreign retained", gecos: retainedMarker + "01ARZ3NDEKTSV4RRFFQ69G5FAW", code: "conflict"},
		{name: "unmarked", code: "conflict"},
		{name: "missing", missing: true, code: "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{gecos: tc.gecos, missing: tc.missing}
			err := VerifyUserForDelete(context.Background(), f, "/srv/sites", "demo", testID)
			var coded *rpcerr.CodedError
			if tc.code == "" && err != nil || tc.code != "" && (!errors.As(err, &coded) || coded.Code != tc.code) {
				t.Fatalf("error = %v, want code %q", err, tc.code)
			}
			if len(f.calls) != 1 || f.calls[0] != "/usr/bin/getent passwd demo" {
				t.Fatalf("not read-only: %v", f.calls)
			}
		})
	}
}

func TestPool(t *testing.T) {
	b, err := RenderPool("demo", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"user = demo", "listen.owner = www-data", "listen.mode = 0660", "pm.max_children = 5"} {
		if !strings.Contains(string(b), v) {
			t.Fatal(v)
		}
	}
	if _, err = RenderPool("demo\nuser=root", 5); err == nil {
		t.Fatal("injection accepted")
	}
}

type deleteReservedFake struct {
	account, group, foreign bool
	groupFailure            bool
	calls                   []string
}

func (f *deleteReservedFake) Run(_ context.Context, p string, a []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	f.calls = append(f.calls, p+" "+strings.Join(a, " "))
	if p == "/usr/sbin/userdel" {
		f.account = false
		return nil, nil, 0, nil
	}
	if p == "/usr/sbin/groupdel" {
		if f.groupFailure {
			f.groupFailure = false
			return nil, nil, 1, errors.New("temporary")
		}
		f.group = false
		return nil, nil, 0, nil
	}
	if p != "/usr/bin/getent" {
		return nil, nil, 1, errors.New("unexpected")
	}
	switch a[0] {
	case "passwd":
		if f.account {
			m := marker + testID
			if f.foreign {
				m = "foreign"
			}
			return []byte("demo:x:2000000:2000000:" + m + ":/srv/sites/demo:/usr/sbin/nologin"), nil, 0, nil
		}
	case "group":
		if f.group {
			return []byte("demo:x:2000000:"), nil, 0, nil
		}
	case "gshadow":
		if f.group {
			return []byte("demo:!" + marker + testID + "::"), nil, 0, nil
		}
	}
	return nil, nil, 2, errors.New("not found")
}
func TestDeleteReservedUserOwnershipAndReplay(t *testing.T) {
	ctx := context.Background()
	f := &deleteReservedFake{account: true, group: true, groupFailure: true}
	if err := DeleteReservedUser(ctx, f, "/srv/sites", "demo", testID, 2000000); err == nil {
		t.Fatal("group deletion failure ignored")
	}
	if f.account || !f.group {
		t.Fatalf("unexpected first state: %+v", f)
	}
	if err := DeleteReservedUser(ctx, f, "/srv/sites", "demo", testID, 2000000); err != nil {
		t.Fatal(err)
	}
	if f.account || f.group {
		t.Fatalf("not removed: %+v", f)
	}
	if err := DeleteReservedUser(ctx, f, "/srv/sites", "demo", testID, 2000000); err != nil {
		t.Fatal("replay", err)
	}
	bad := &deleteReservedFake{account: true, group: true, foreign: true}
	if err := DeleteReservedUser(ctx, bad, "/srv/sites", "demo", testID, 2000000); err == nil {
		t.Fatal("foreign account accepted")
	}
	if strings.Contains(strings.Join(bad.calls, "\n"), "userdel") {
		t.Fatal("foreign account deleted")
	}
}
