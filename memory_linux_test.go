//go:build linux

package main

import (
	"strings"
	"testing"
)

func TestParseProcMeminfo(t *testing.T) {
	input := `MemTotal:       16384000 kB
MemFree:         1024000 kB
MemAvailable:    8192000 kB
Buffers:          100000 kB
`
	info, err := parseProcMeminfo(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if want := uint64(16384000 * 1024); info.TotalBytes != want {
		t.Fatalf("TotalBytes = %d, want %d", info.TotalBytes, want)
	}
	if want := uint64(8192000 * 1024); info.AvailableBytes != want {
		t.Fatalf("AvailableBytes = %d, want %d", info.AvailableBytes, want)
	}
}

func TestParseProcMeminfoFallsBackToFree(t *testing.T) {
	input := "MemTotal: 4096 kB\nMemFree: 512 kB\n"
	info, err := parseProcMeminfo(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if info.AvailableBytes != 512*1024 {
		t.Fatalf("AvailableBytes = %d", info.AvailableBytes)
	}
}

func TestParseProcMeminfoRequiresTotal(t *testing.T) {
	if _, err := parseProcMeminfo(strings.NewReader("MemFree: 512 kB\n")); err == nil {
		t.Fatal("expected missing MemTotal error")
	}
}
