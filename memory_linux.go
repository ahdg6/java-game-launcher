//go:build linux

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func detectPlatformMemory() (MemoryInfo, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return MemoryInfo{}, fmt.Errorf("open /proc/meminfo: %w", err)
	}
	defer file.Close()
	return parseProcMeminfo(file)
}

func parseProcMeminfo(reader io.Reader) (MemoryInfo, error) {
	var info MemoryInfo
	var freeBytes uint64
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		var target *uint64
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			target = &info.TotalBytes
		case "MemAvailable":
			target = &info.AvailableBytes
		case "MemFree":
			target = &freeBytes
		default:
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return MemoryInfo{}, fmt.Errorf("parse %s: %w", fields[0], err)
		}
		// /proc/meminfo values are conventionally expressed in KiB. Support a
		// byte value as well so malformed or synthetic input cannot be inflated.
		if len(fields) >= 3 && strings.EqualFold(fields[2], "kB") {
			if value > ^uint64(0)/1024 {
				return MemoryInfo{}, fmt.Errorf("%s overflows bytes", fields[0])
			}
			value *= 1024
		}
		*target = value
	}
	if err := scanner.Err(); err != nil {
		return MemoryInfo{}, fmt.Errorf("read /proc/meminfo: %w", err)
	}
	if info.TotalBytes == 0 {
		return MemoryInfo{}, fmt.Errorf("MemTotal missing from /proc/meminfo")
	}
	if info.AvailableBytes == 0 {
		info.AvailableBytes = freeBytes
	}
	return info, nil
}
