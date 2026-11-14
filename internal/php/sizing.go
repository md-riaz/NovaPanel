package php

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// ChildrenCap computes the frozen creation-time cap. Unknown memory fails safe.
func ChildrenCap(memoryMB, activeSites int) int {
	if memoryMB <= 0 {
		return 2
	}
	if activeSites < 1 {
		activeSites = 1
	}
	budget := memoryMB / 2
	if budget < 256 {
		budget = 256
	}
	cap := (budget - 256) / 64 / activeSites
	if cap < 2 {
		return 2
	}
	if cap > 32 {
		return 32
	}
	return cap
}

// TotalMemoryMB reads Linux's physical memory, not currently available memory.
func TotalMemoryMB() int {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 3 && fields[0] == "MemTotal:" && fields[2] == "kB" {
			kb, err := strconv.Atoi(fields[1])
			if err == nil && kb > 0 {
				return kb / 1024
			}
			return 0
		}
	}
	return 0
}
