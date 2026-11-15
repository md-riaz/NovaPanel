package php

import (
	"strings"
	"testing"

	"novapanel/internal/store"
)

func TestPoolLimits(t *testing.T) {
	memory, upload, exec := 256, 32, 60
	b, err := RenderPoolLimits("demo", 7, store.PHPLimits{MemoryMB: &memory, UploadMB: &upload, MaxExecSec: &exec})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pm.max_children = 7", "php_admin_value[memory_limit] = 256M", "php_admin_value[upload_max_filesize] = 32M", "php_admin_value[post_max_size] = 32M", "php_admin_value[max_execution_time] = 60"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q", want)
		}
	}
	defaults, err := RenderPoolLimits("demo", 1, store.PHPLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(defaults), "memory_limit") || strings.Contains(string(defaults), "upload_max_filesize") || strings.Contains(string(defaults), "max_execution_time") {
		t.Fatal("overrode runtime defaults")
	}
	for _, n := range []int{-1, 0, 1000001} {
		for _, limits := range []store.PHPLimits{{MemoryMB: &n}, {UploadMB: &n}, {MaxExecSec: &n}} {
			if _, err := RenderPoolLimits("demo", 1, limits); err == nil {
				t.Errorf("accepted %d: %+v", n, limits)
			}
		}
	}
	for _, cap := range []int{0, 10001} {
		if _, err := RenderPoolLimits("demo", cap, store.PHPLimits{}); err == nil {
			t.Errorf("accepted cap %d", cap)
		}
	}
}
