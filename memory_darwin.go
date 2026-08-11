//go:build darwin

package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func detectPlatformMemory() (MemoryInfo, error) {
	output, err := exec.Command("/usr/sbin/sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return MemoryInfo{}, fmt.Errorf("read hw.memsize: %w", err)
	}
	total, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return MemoryInfo{}, fmt.Errorf("parse hw.memsize %q: %w", strings.TrimSpace(string(output)), err)
	}
	if total == 0 {
		return MemoryInfo{}, fmt.Errorf("hw.memsize returned zero physical memory")
	}
	return MemoryInfo{TotalBytes: total}, nil
}
