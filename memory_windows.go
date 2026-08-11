//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

var globalMemoryStatusEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

type windowsMemoryStatusEx struct {
	length            uint32
	memoryLoad        uint32
	totalPhysical     uint64
	availablePhysical uint64
	totalPageFile     uint64
	availablePageFile uint64
	totalVirtual      uint64
	availableVirtual  uint64
	availableExtended uint64
}

func detectPlatformMemory() (MemoryInfo, error) {
	status := windowsMemoryStatusEx{}
	status.length = uint32(unsafe.Sizeof(status))
	result, _, callErr := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if result == 0 {
		return MemoryInfo{}, fmt.Errorf("GlobalMemoryStatusEx: %w", callErr)
	}
	if status.totalPhysical == 0 {
		return MemoryInfo{}, fmt.Errorf("GlobalMemoryStatusEx returned zero physical memory")
	}
	return MemoryInfo{
		TotalBytes:     status.totalPhysical,
		AvailableBytes: status.availablePhysical,
	}, nil
}
