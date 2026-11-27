package platform

import "testing"

func TestOwnCgroup(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"0::/system.slice/novapanel.service\n", "/system.slice/novapanel.service"},
		{"0::/\n", "/"},
	} {
		got, err := ownCgroup(tc.input)
		if err != nil || got != tc.want {
			t.Fatalf("%q: %q %v", tc.input, got, err)
		}
	}
	for _, input := range []string{"", "0::relative\n", "0::/../escape\n", "0::/a\n0::/b\n", "0::/a/../b\n"} {
		if _, err := ownCgroup(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}
