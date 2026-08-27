//go:build !linux && !darwin && !windows

package java

import "fmt"

func detectPlatformMemory() (MemoryInfo, error) {
	return MemoryInfo{}, fmt.Errorf("physical memory detection is unsupported on this platform")
}
