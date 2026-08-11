package main

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestScanMindustryModsReadsPackagesAndMetadata(t *testing.T) {
	dataDir := t.TempDir()
	modsDir := filepath.Join(dataDir, "mods")

	directory := filepath.Join(modsDir, "source-mod")
	writeModTestFile(t, filepath.Join(directory, "mod.json"), `{
  "name": "source-name",
  "displayName": "Source Display",
  "version": 2.5,
  "author": "Alice"
}`)
	writeModTestFile(t, filepath.Join(directory, "content", "payload.txt"), "payload")

	jarPath := filepath.Join(modsDir, "plugin.jar")
	createModTestArchive(t, jarPath, map[string]string{
		"mod.json":    `{this metadata file is malformed}`,
		"plugin.json": `{"name":"plugin-name","displayName":"Plugin Display","version":"1.2","author":"Bob"}`,
		"binary.dat":  "1234567890",
	})
	disabledPath := filepath.Join(modsDir, "packed.zip.disabled")
	createModTestArchive(t, disabledPath, map[string]string{
		"packed/mod.hjson": `name: packed-name
displayName: Packed Display
version: v3
author: Carol # release author
`,
	})
	writeModTestFile(t, filepath.Join(modsDir, "not-a-mod.txt"), "ignored")

	outside := filepath.Join(t.TempDir(), "outside.jar")
	writeModTestFile(t, outside, "not a real archive")
	symlinkCreated := false
	if err := os.Symlink(outside, filepath.Join(modsDir, "linked.jar")); err == nil {
		symlinkCreated = true
	} else if runtime.GOOS != "windows" {
		t.Fatalf("创建测试符号链接：%v", err)
	}

	mods, err := ScanMindustryMods(dataDir)
	if err != nil {
		t.Fatalf("ScanMindustryMods() error = %v", err)
	}
	if len(mods) != 3 {
		t.Fatalf("len(mods) = %d, want 3: %#v", len(mods), mods)
	}

	byFile := make(map[string]MindustryMod, len(mods))
	for _, mod := range mods {
		byFile[filepath.Base(mod.Path)] = mod
		if !samePath(filepath.Dir(mod.Path), modsDir) {
			t.Errorf("mod path %q is not under %q", mod.Path, modsDir)
		}
		if mod.Size <= 0 {
			t.Errorf("mod %q Size = %d, want positive", mod.Path, mod.Size)
		}
	}

	source := byFile["source-mod"]
	if source.Name != "source-name" || source.DisplayName != "Source Display" || source.Version != "2.5" || source.Author != "Alice" {
		t.Errorf("directory metadata = %#v", source)
	}
	if source.Type != MindustryModDirectory || !source.Enabled {
		t.Errorf("directory package state = %#v", source)
	}

	plugin := byFile["plugin.jar"]
	if plugin.Name != "plugin-name" || plugin.DisplayName != "Plugin Display" || plugin.Version != "1.2" || plugin.Author != "Bob" {
		t.Errorf("JAR metadata = %#v", plugin)
	}
	if plugin.Type != MindustryModJAR || !plugin.Enabled {
		t.Errorf("JAR package state = %#v", plugin)
	}

	packed := byFile["packed.zip.disabled"]
	if packed.Name != "packed-name" || packed.DisplayName != "Packed Display" || packed.Version != "v3" || packed.Author != "Carol" {
		t.Errorf("HJSON metadata = %#v", packed)
	}
	if packed.Type != MindustryModZIP || packed.Enabled {
		t.Errorf("disabled ZIP package state = %#v", packed)
	}
	if symlinkCreated {
		if _, found := byFile["linked.jar"]; found {
			t.Error("ScanMindustryMods() included a symlink")
		}
	}
}

func TestScanMindustryModsMissingDirectoryIsEmpty(t *testing.T) {
	dataDir := t.TempDir()
	mods, err := ScanMindustryMods(dataDir)
	if err != nil {
		t.Fatalf("ScanMindustryMods() error = %v", err)
	}
	if mods == nil || len(mods) != 0 {
		t.Fatalf("mods = %#v, want a non-nil empty slice", mods)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "mods")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scan unexpectedly created mods directory, stat error = %v", err)
	}
}

func TestScanMindustryModsUsesFileNameFallback(t *testing.T) {
	dataDir := t.TempDir()
	writeModTestFile(t, filepath.Join(dataDir, "mods", "broken.JAR.disabled"), "not a zip")

	mods, err := ScanMindustryMods(dataDir)
	if err != nil {
		t.Fatalf("ScanMindustryMods() error = %v", err)
	}
	if len(mods) != 1 {
		t.Fatalf("mods = %#v", mods)
	}
	if mods[0].Name != "broken" || mods[0].DisplayName != "broken" || mods[0].Type != MindustryModJAR || mods[0].Enabled {
		t.Fatalf("fallback mod = %#v", mods[0])
	}
}

func TestSetMindustryModEnabledRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	createModTestArchive(t, filepath.Join(dataDir, "mods", "roundtrip.jar"), map[string]string{
		"mod.json": `{"name":"roundtrip"}`,
	})
	writeModTestFile(t, filepath.Join(dataDir, "mods", "folder", "mod.hjson"), "name: folder\n")

	mods, err := ScanMindustryMods(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, original := range mods {
		disabled, err := SetMindustryModEnabled(original, false)
		if err != nil {
			t.Fatalf("disable %s: %v", original.Name, err)
		}
		if disabled.Enabled || !hasSuffixFold(disabled.Path, ".disabled") {
			t.Fatalf("disabled mod = %#v", disabled)
		}
		if _, err := os.Lstat(original.Path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("original path remains after disable: %v", err)
		}
		if _, err := os.Lstat(disabled.Path); err != nil {
			t.Fatalf("disabled path missing: %v", err)
		}

		restored, err := SetMindustryModEnabled(disabled, true)
		if err != nil {
			t.Fatalf("enable %s: %v", original.Name, err)
		}
		if !restored.Enabled || !samePath(restored.Path, original.Path) {
			t.Fatalf("restored mod = %#v, original = %#v", restored, original)
		}
	}
}

func TestSetMindustryModEnabledRejectsConflict(t *testing.T) {
	dataDir := t.TempDir()
	modsDir := filepath.Join(dataDir, "mods")
	writeModTestFile(t, filepath.Join(modsDir, "same.jar"), "enabled")
	writeModTestFile(t, filepath.Join(modsDir, "same.jar.disabled"), "disabled collision")

	mods, err := ScanMindustryMods(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	enabled := findModTestByFile(t, mods, "same.jar")
	if _, err := SetMindustryModEnabled(enabled, false); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("SetMindustryModEnabled() error = %v, want conflict", err)
	}
	if got := string(readModTestFile(t, filepath.Join(modsDir, "same.jar"))); got != "enabled" {
		t.Fatalf("enabled file changed to %q", got)
	}
	if got := string(readModTestFile(t, filepath.Join(modsDir, "same.jar.disabled"))); got != "disabled collision" {
		t.Fatalf("conflicting file changed to %q", got)
	}
}

func TestSetMindustryModEnabledRejectsPathEscapeAndSymlink(t *testing.T) {
	dataDir := t.TempDir()
	modsDir := filepath.Join(dataDir, "mods")
	writeModTestFile(t, filepath.Join(modsDir, "safe.jar"), "safe")
	mods, err := ScanMindustryMods(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	mod := mods[0]

	outside := filepath.Join(t.TempDir(), "outside.jar")
	writeModTestFile(t, outside, "outside")
	escaped := mod
	escaped.Path = outside
	if _, err := SetMindustryModEnabled(escaped, false); err == nil || !strings.Contains(err.Error(), "越界") {
		t.Fatalf("outside path error = %v, want path escape rejection", err)
	}
	if got := string(readModTestFile(t, outside)); got != "outside" {
		t.Fatalf("outside file changed to %q", got)
	}

	nestedPath := filepath.Join(modsDir, "nested", "nested.jar")
	writeModTestFile(t, nestedPath, "nested")
	nested := mod
	nested.Path = nestedPath
	if _, err := SetMindustryModEnabled(nested, false); err == nil || !strings.Contains(err.Error(), "越界") {
		t.Fatalf("nested path error = %v, want direct-child rejection", err)
	}

	if err := os.Remove(mod.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, mod.Path); err == nil {
		if _, err := SetMindustryModEnabled(mod, false); err == nil || !strings.Contains(err.Error(), "符号链接") {
			t.Fatalf("symlink error = %v, want symlink rejection", err)
		}
	} else if runtime.GOOS != "windows" {
		t.Fatalf("创建测试符号链接：%v", err)
	}
}

func TestDisableAllMindustryModsAndRestore(t *testing.T) {
	dataDir := t.TempDir()
	modsDir := filepath.Join(dataDir, "mods")
	writeModTestFile(t, filepath.Join(modsDir, "alpha.jar"), "alpha")
	writeModTestFile(t, filepath.Join(modsDir, "beta", "mod.json"), `{"name":"beta"}`)
	writeModTestFile(t, filepath.Join(modsDir, "already.zip.disabled"), "already disabled")

	mods, err := ScanMindustryMods(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := DisableAllMindustryMods(mods)
	if err != nil {
		t.Fatalf("DisableAllMindustryMods() error = %v", err)
	}
	if len(plan.Changes) != 2 {
		t.Fatalf("len(plan.Changes) = %d, want 2", len(plan.Changes))
	}
	for _, change := range plan.Changes {
		if _, err := os.Lstat(change.Before.Path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("enabled path %q remains: %v", change.Before.Path, err)
		}
		if _, err := os.Lstat(change.After.Path); err != nil {
			t.Errorf("disabled path %q missing: %v", change.After.Path, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(modsDir, "already.zip.disabled")); err != nil {
		t.Fatalf("already-disabled mod was changed: %v", err)
	}

	if err := RestoreMindustryMods(plan); err != nil {
		t.Fatalf("RestoreMindustryMods() error = %v", err)
	}
	for _, change := range plan.Changes {
		if _, err := os.Lstat(change.Before.Path); err != nil {
			t.Errorf("restored path %q missing: %v", change.Before.Path, err)
		}
		if _, err := os.Lstat(change.After.Path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("disabled path %q remains: %v", change.After.Path, err)
		}
	}
}

func TestDisableAllMindustryModsConflictMakesNoPartialChanges(t *testing.T) {
	dataDir := t.TempDir()
	modsDir := filepath.Join(dataDir, "mods")
	writeModTestFile(t, filepath.Join(modsDir, "alpha.jar"), "alpha")
	writeModTestFile(t, filepath.Join(modsDir, "zeta.jar"), "zeta")
	writeModTestFile(t, filepath.Join(modsDir, "zeta.jar.disabled"), "conflict")

	mods, err := ScanMindustryMods(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DisableAllMindustryMods(mods); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("DisableAllMindustryMods() error = %v, want conflict", err)
	}
	for _, name := range []string{"alpha.jar", "zeta.jar", "zeta.jar.disabled"} {
		if _, err := os.Lstat(filepath.Join(modsDir, name)); err != nil {
			t.Errorf("%s changed despite failed plan: %v", name, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(modsDir, "alpha.jar.disabled")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("alpha was partially disabled, stat error = %v", err)
	}
}

func TestRestoreMindustryModsConflictLeavesAllDisabled(t *testing.T) {
	dataDir := t.TempDir()
	modsDir := filepath.Join(dataDir, "mods")
	writeModTestFile(t, filepath.Join(modsDir, "alpha.jar"), "alpha")
	writeModTestFile(t, filepath.Join(modsDir, "beta.jar"), "beta")
	mods, err := ScanMindustryMods(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := DisableAllMindustryMods(mods)
	if err != nil {
		t.Fatal(err)
	}
	writeModTestFile(t, filepath.Join(modsDir, "alpha.jar"), "new conflicting mod")

	if err := RestoreMindustryMods(plan); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("RestoreMindustryMods() error = %v, want conflict", err)
	}
	for _, name := range []string{"alpha.jar.disabled", "beta.jar.disabled"} {
		if _, err := os.Lstat(filepath.Join(modsDir, name)); err != nil {
			t.Errorf("%s not preserved after refused restore: %v", name, err)
		}
	}
	if got := string(readModTestFile(t, filepath.Join(modsDir, "alpha.jar"))); got != "new conflicting mod" {
		t.Fatalf("conflicting target changed to %q", got)
	}
}

func findModTestByFile(t *testing.T, mods []MindustryMod, name string) MindustryMod {
	t.Helper()
	for _, mod := range mods {
		if filepath.Base(mod.Path) == name {
			return mod
		}
	}
	t.Fatalf("mod %q not found in %#v", name, mods)
	return MindustryMod{}
}

func writeModTestFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readModTestFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func createModTestArchive(t *testing.T, name string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	archive, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(archive)
	for path, content := range files {
		entry, err := writer.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
}
