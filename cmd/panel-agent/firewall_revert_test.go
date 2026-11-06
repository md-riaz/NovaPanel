package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFirewallDispatcher(t *testing.T) {
	const path = "/var/lib/novapanel/firewall/snapshots/01ARZ3NDEKTSV4RRFFQ69G5FAV"
	sentinel := errors.New("recovery reached")
	for _, tc := range []struct {
		name     string
		args     []string
		uid, gid int
		reach    bool
	}{
		{"valid", []string{"fw-revert", path}, 0, 0, true},
		{"uid", []string{"fw-revert", path}, 1, 0, false},
		{"gid", []string{"fw-revert", path}, 0, 1, false},
		{"missing", []string{"fw-revert"}, 0, 0, false},
		{"extra", []string{"fw-revert", path, "--config=x"}, 0, 0, false},
		{"relative", []string{"fw-revert", strings.TrimPrefix(path, "/")}, 0, 0, false},
		{"slash", []string{"fw-revert", path + "/"}, 0, 0, false},
		{"dot", []string{"fw-revert", strings.Replace(path, "/snapshots/", "/./snapshots/", 1)}, 0, 0, false},
		{"double", []string{"fw-revert", strings.Replace(path, "/snapshots/", "//snapshots/", 1)}, 0, 0, false},
		{"lower", []string{"fw-revert", strings.ToLower(path)}, 0, 0, false},
		{"prefix", []string{"fw-revert", "/etc/" + path}, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			handled, err := dispatchFirewallRevert(context.Background(), tc.args, tc.uid, tc.gid, func(_ context.Context, id string) error {
				calls++
				if id != "01ARZ3NDEKTSV4RRFFQ69G5FAV" {
					t.Fatal(id)
				}
				return sentinel
			})
			if !handled || err == nil || (calls == 1) != tc.reach || errors.Is(err, sentinel) != tc.reach {
				t.Fatalf("handled=%v calls=%d err=%v", handled, calls, err)
			}
		})
	}
	handled, err := dispatchFirewallRevert(context.Background(), nil, 0, 0, nil)
	if handled || err != nil {
		t.Fatal("daemon invocation intercepted", err)
	}
	handled, err = dispatchFirewallRevert(context.Background(), []string{"unknown"}, 0, 0, nil)
	if handled || err != nil {
		t.Fatal("daemon argument handling intercepted", err)
	}
}
