package php

import "novapanel/internal/rpcerr"

// Limits is a complete PHP quota tuple. Units are MiB and seconds.
type Limits struct {
	MemoryMB   int `json:"memory_mb"`
	UploadMB   int `json:"upload_mb"`
	MaxExecSec int `json:"max_exec_sec"`
}

// LimitsPatch preserves omitted fields; an explicit zero is not a default.
type LimitsPatch struct {
	MemoryMB   *int `json:"memory_mb,omitempty"`
	UploadMB   *int `json:"upload_mb,omitempty"`
	MaxExecSec *int `json:"max_exec_sec,omitempty"`
}

// MergeLimits uses canonical creation defaults only when current is nil.
func MergeLimits(current *Limits, patch LimitsPatch) (Limits, error) {
	merged := Limits{MemoryMB: 256, UploadMB: 64, MaxExecSec: 60}
	if current != nil {
		merged = *current
	}
	if patch.MemoryMB != nil {
		merged.MemoryMB = *patch.MemoryMB
	}
	if patch.UploadMB != nil {
		merged.UploadMB = *patch.UploadMB
	}
	if patch.MaxExecSec != nil {
		merged.MaxExecSec = *patch.MaxExecSec
	}
	if merged.MemoryMB < 32 || merged.MemoryMB > 2048 || merged.UploadMB < 1 || merged.UploadMB > 1024 || merged.MaxExecSec < 1 || merged.MaxExecSec > 600 || merged.UploadMB > merged.MemoryMB {
		return Limits{}, rpcerr.Errf("invalid_argument", "Invalid PHP limits")
	}
	return merged, nil
}
