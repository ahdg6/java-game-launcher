package main

import (
	"reflect"
	"testing"
)

func TestResolveJVMPresetAutoHeap(t *testing.T) {
	tests := []struct {
		name  string
		total uint64
		heap  string
	}{
		{name: "unknown falls back to balanced", total: 0, heap: "2g"},
		{name: "small system", total: 4 * gibibyte, heap: "1g"},
		{name: "just below eight", total: 8*gibibyte - 1, heap: "1g"},
		{name: "eight gibibytes", total: 8 * gibibyte, heap: "2g"},
		{name: "just below sixteen", total: 16*gibibyte - 1, heap: "2g"},
		{name: "sixteen gibibytes", total: 16 * gibibyte, heap: "4g"},
		{name: "large system", total: 64 * gibibyte, heap: "4g"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			preset := ResolveJVMPreset(presetAuto, MemoryInfo{TotalBytes: test.total})
			want := []string{"-Xms" + test.heap, "-Xmx" + test.heap}
			if !reflect.DeepEqual(preset.Args[:2], want) {
				t.Fatalf("heap args = %#v, want %#v", preset.Args[:2], want)
			}
		})
	}
}

func TestJVMPresetsKeepLowPauseG1Core(t *testing.T) {
	wantTail := []string{
		"-XX:+UseG1GC",
		"-XX:MaxGCPauseMillis=30",
		"-XX:G1ReservePercent=20",
		"-XX:+ParallelRefProcEnabled",
		"-XX:+AlwaysPreTouch",
		"-XX:+DisableExplicitGC",
		"-Dfile.encoding=UTF-8",
	}
	for _, preset := range AvailableJVMPresets(MemoryInfo{TotalBytes: 16 * gibibyte}) {
		if len(preset.Args) < 2 || preset.Args[0][4:] != preset.Args[1][4:] {
			t.Fatalf("%s does not use a fixed-size heap: %#v", preset.ID, preset.Args)
		}
		if !reflect.DeepEqual(preset.Args[2:], wantTail) {
			t.Fatalf("%s core args = %#v, want %#v", preset.ID, preset.Args[2:], wantTail)
		}
	}
}

func TestJVMPresetArgsAreIndependent(t *testing.T) {
	first := ResolveJVMPreset(presetBalanced, MemoryInfo{})
	first.Args[0] = "changed"
	second := ResolveJVMPreset(presetBalanced, MemoryInfo{})
	if second.Args[0] != "-Xms2g" {
		t.Fatalf("preset args leaked between calls: %#v", second.Args)
	}

	list := AvailableJVMPresets(MemoryInfo{TotalBytes: 16 * gibibyte})
	list[0].Args[0] = "changed"
	if list[1].Args[0] == "changed" {
		t.Fatal("available presets share argument storage")
	}
}

func TestResolveUnknownJVMPresetUsesBalanced(t *testing.T) {
	preset := ResolveJVMPreset("does-not-exist", MemoryInfo{TotalBytes: 64 * gibibyte})
	if preset.ID != presetBalanced || preset.Args[0] != "-Xms2g" {
		t.Fatalf("unknown preset resolved to %#v", preset)
	}
}

func TestAvailableJVMPresetOrder(t *testing.T) {
	presets := AvailableJVMPresets(MemoryInfo{})
	want := []string{presetAuto, presetConservative, presetBalanced, presetPerformance}
	got := make([]string, len(presets))
	for i, preset := range presets {
		got[i] = preset.ID
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("preset IDs = %#v, want %#v", got, want)
	}
}
