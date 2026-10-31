package php_test

import (
	"errors"
	"fmt"
	"novapanel/internal/render/php"
	"novapanel/internal/rpcerr"
	"testing"
)

func TestPhase4PHPLimitsMerge(t *testing.T) {
	got, err := php.MergeLimits(nil, php.LimitsPatch{})
	if err != nil || got != (php.Limits{MemoryMB: 256, UploadMB: 64, MaxExecSec: 60}) {
		t.Fatalf("defaults: %+v, %v", got, err)
	}
	t.Run("partial merge preserves omitted values", func(t *testing.T) {
		current := php.Limits{MemoryMB: 512, UploadMB: 128, MaxExecSec: 120}
		before := current
		memory := 1024
		got, err := php.MergeLimits(&current, php.LimitsPatch{MemoryMB: &memory})
		if err != nil || got != (php.Limits{MemoryMB: 1024, UploadMB: 128, MaxExecSec: 120}) || current != before || memory != 1024 {
			t.Fatalf("merge: %+v, %v; current %+v", got, err, current)
		}
		upload, seconds := 256, 300
		got, err = php.MergeLimits(&current, php.LimitsPatch{UploadMB: &upload, MaxExecSec: &seconds})
		if err != nil || got != (php.Limits{MemoryMB: 512, UploadMB: 256, MaxExecSec: 300}) || current != before {
			t.Fatalf("merge: %+v, %v", got, err)
		}
		got, err = php.MergeLimits(&current, php.LimitsPatch{})
		if err != nil || got != current {
			t.Fatalf("empty patch: %+v, %v", got, err)
		}
	})
	t.Run("bounds and merged tuple", func(t *testing.T) {
		for _, memory := range []int{31, 32, 2048, 2049} {
			for _, upload := range []int{0, 1, 32, 1024, 1025} {
				for _, seconds := range []int{0, 1, 600, 601} {
					t.Run(fmt.Sprintf("%d-%d-%d", memory, upload, seconds), func(t *testing.T) {
						current := php.Limits{MemoryMB: 512, UploadMB: 128, MaxExecSec: 120}
						before := current
						got, err := php.MergeLimits(&current, php.LimitsPatch{MemoryMB: &memory, UploadMB: &upload, MaxExecSec: &seconds})
						valid := memory >= 32 && memory <= 2048 && upload >= 1 && upload <= 1024 && seconds >= 1 && seconds <= 600 && upload <= memory
						if valid {
							if err != nil || got != (php.Limits{MemoryMB: memory, UploadMB: upload, MaxExecSec: seconds}) {
								t.Fatalf("valid merge: %+v, %v", got, err)
							}
						} else {
							var coded *rpcerr.CodedError
							if !errors.As(err, &coded) || coded.Code != "invalid_argument" || got != (php.Limits{}) {
								t.Fatalf("invalid merge: %+v, %v", got, err)
							}
						}
						if current != before {
							t.Fatal("mutated current")
						}
					})
				}
			}
		}
		for _, tc := range []struct {
			name    string
			current *php.Limits
			patch   php.LimitsPatch
		}{
			{"memory-only below retained upload", &php.Limits{MemoryMB: 512, UploadMB: 128, MaxExecSec: 120}, php.LimitsPatch{MemoryMB: intPointer(64)}},
			{"upload-only above retained memory", &php.Limits{MemoryMB: 256, UploadMB: 64, MaxExecSec: 60}, php.LimitsPatch{UploadMB: intPointer(257)}},
			{"new site memory below default upload", nil, php.LimitsPatch{MemoryMB: intPointer(32)}},
			{"explicit zero", nil, php.LimitsPatch{MaxExecSec: intPointer(0)}},
			{"negative", nil, php.LimitsPatch{MemoryMB: intPointer(-1)}},
			{"invalid retained tuple", &php.Limits{}, php.LimitsPatch{}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got, err := php.MergeLimits(tc.current, tc.patch)
				var coded *rpcerr.CodedError
				if !errors.As(err, &coded) || coded.Code != "invalid_argument" || got != (php.Limits{}) {
					t.Fatalf("invalid merge: %+v, %v", got, err)
				}
			})
		}
		current := php.Limits{MemoryMB: 256, UploadMB: 64, MaxExecSec: 60}
		got, err := php.MergeLimits(&current, php.LimitsPatch{MemoryMB: intPointer(32), UploadMB: intPointer(32)})
		if err != nil || got != (php.Limits{MemoryMB: 32, UploadMB: 32, MaxExecSec: 60}) {
			t.Fatalf("validate after full merge: %+v, %v", got, err)
		}
	})
}

func intPointer(n int) *int { return &n }
