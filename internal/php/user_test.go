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
