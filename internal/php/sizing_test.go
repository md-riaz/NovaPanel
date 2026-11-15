package php

import "testing"

func TestChildrenCap(t *testing.T) {
	for _, tc := range []struct{ memory, sites, want int }{
		{0, 1, 2}, {512, 1, 2}, {4096, 1, 28}, {4096, 4, 7}, {65536, 1, 32},
	} {
		if got := ChildrenCap(tc.memory, tc.sites); got != tc.want {
			t.Errorf("ChildrenCap(%d,%d)=%d want %d", tc.memory, tc.sites, got, tc.want)
		}
	}
}
