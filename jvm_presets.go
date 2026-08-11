package main

import "fmt"

const (
	presetAuto         = "auto"
	presetConservative = "conservative"
	presetBalanced     = "balanced"
	presetPerformance  = "performance"
)

// MemoryInfo describes host physical memory. AvailableBytes may be zero on
// platforms where the operating system does not expose it cheaply. A zero
// TotalBytes means that detection was not supported or failed.
type MemoryInfo struct {
	TotalBytes     uint64
	AvailableBytes uint64
}

// JVMPreset is a reusable, UI-independent JVM configuration. Args is owned by
// the returned value: callers may edit it without changing future results.
type JVMPreset struct {
	ID          string
	Name        string
	Description string
	Args        []string
}

// DetectMemory returns the physical memory reported by the host operating
// system. Detection failure is represented by an empty MemoryInfo so callers
// can safely fall back without making startup fatal.
func DetectMemory() MemoryInfo {
	info, err := detectPlatformMemory()
	if err != nil || info.TotalBytes == 0 {
		return MemoryInfo{}
	}
	if info.AvailableBytes > info.TotalBytes {
		info.AvailableBytes = info.TotalBytes
	}
	return info
}

// ResolveJVMPreset resolves a preset ID into concrete JVM arguments. Auto uses
// total physical memory: under 8 GiB gets a 1 GiB heap, 8-16 GiB gets 2 GiB,
// and 16 GiB or more gets 4 GiB. Unknown memory and unknown IDs use Balanced.
func ResolveJVMPreset(id string, memory MemoryInfo) JVMPreset {
	switch id {
	case presetConservative:
		return newJVMPreset(
			presetConservative,
			"保守（1 GiB）",
			"适合内存较小的电脑，为系统和其他程序保留更多内存。",
			"1g",
		)
	case presetPerformance:
		return newJVMPreset(
			presetPerformance,
			"性能（4 GiB）",
			"适合 16 GiB 以上内存和较多模组，减少高负载时的回收压力。",
			"4g",
		)
	case presetAuto:
		heap := autoHeapSize(memory.TotalBytes)
		description := "未能检测物理内存，已采用均衡的 2 GiB 堆内存。"
		if memory.TotalBytes != 0 {
			description = fmt.Sprintf(
				"检测到 %.1f GiB 物理内存，自动使用 %s 堆内存。",
				float64(memory.TotalBytes)/float64(gibibyte),
				heapLabel(heap),
			)
		}
		return newJVMPreset(presetAuto, "自动", description, heap)
	case presetBalanced:
		fallthrough
	default:
		return newJVMPreset(
			presetBalanced,
			"均衡（2 GiB）",
			"适合大多数 Mindustry 客户端，在内存占用和 GC 余量间取得平衡。",
			"2g",
		)
	}
}

// AvailableJVMPresets returns all choices in a UI-friendly order. Every call
// owns fresh argument slices, including the resolved Auto arguments.
func AvailableJVMPresets(memory MemoryInfo) []JVMPreset {
	return []JVMPreset{
		ResolveJVMPreset(presetAuto, memory),
		ResolveJVMPreset(presetConservative, memory),
		ResolveJVMPreset(presetBalanced, memory),
		ResolveJVMPreset(presetPerformance, memory),
	}
}

const gibibyte = uint64(1024 * 1024 * 1024)

func autoHeapSize(totalBytes uint64) string {
	switch {
	case totalBytes == 0:
		return "2g"
	case totalBytes < 8*gibibyte:
		return "1g"
	case totalBytes < 16*gibibyte:
		return "2g"
	default:
		return "4g"
	}
}

func heapLabel(heap string) string {
	switch heap {
	case "1g":
		return "1 GiB"
	case "4g":
		return "4 GiB"
	default:
		return "2 GiB"
	}
}

func newJVMPreset(id, name, description, heap string) JVMPreset {
	return JVMPreset{
		ID:          id,
		Name:        name,
		Description: description,
		Args: []string{
			"-Xms" + heap,
			"-Xmx" + heap,
			"-XX:+UseG1GC",
			"-XX:MaxGCPauseMillis=30",
			"-XX:G1ReservePercent=20",
			"-XX:+ParallelRefProcEnabled",
			"-XX:+AlwaysPreTouch",
			"-XX:+DisableExplicitGC",
			"-Dfile.encoding=UTF-8",
		},
	}
}
