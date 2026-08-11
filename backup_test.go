package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestCreateDataBackupPreservesUserDataAndExcludesRegenerableData(t *testing.T) {
	dataDir := t.TempDir()
	writeBackupTestFile(t, filepath.Join(dataDir, "saves", "slot.msav"), "save")
	writeBackupTestFile(t, filepath.Join(dataDir, "mods", "example.jar"), "mod")
	writeBackupTestFile(t, filepath.Join(dataDir, "maps", "island.msav"), "map")
	writeBackupTestFile(t, filepath.Join(dataDir, "settings.bin"), "settings")
	writeBackupTestFile(t, filepath.Join(dataDir, "my-unknown-data", "notes.txt"), "keep me")
	// Only known top-level caches are excluded; similarly named directories
	// nested in unknown user data are preserved conservatively.
	writeBackupTestFile(t, filepath.Join(dataDir, "my-unknown-data", "cache", "manual.txt"), "also keep")
	writeBackupTestFile(t, filepath.Join(dataDir, "cache", "textures.bin"), strings.Repeat("x", 1024))
	writeBackupTestFile(t, filepath.Join(dataDir, "tmp", "download.part"), "temporary")
	if err := os.Mkdir(filepath.Join(dataDir, "empty-user-directory"), 0o755); err != nil {
		t.Fatal(err)
	}

	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeBackupTestFile(t, outside, "must not leak")
	symlinkCreated := false
	if err := os.Symlink(outside, filepath.Join(dataDir, "outside-link")); err == nil {
		symlinkCreated = true
	} else if runtime.GOOS != "windows" {
		t.Fatalf("创建测试符号链接：%v", err)
	}

	backupDir := t.TempDir()
	result, err := CreateDataBackup(dataDir, backupDir)
	if err != nil {
		t.Fatalf("CreateDataBackup() error = %v", err)
	}
	if result.FileCount != 6 {
		t.Fatalf("FileCount = %d, want 6", result.FileCount)
	}
	if result.ArchiveBytes <= 0 || result.SourceBytes <= 0 {
		t.Fatalf("unexpected sizes: archive=%d source=%d", result.ArchiveBytes, result.SourceBytes)
	}
	if filepath.Dir(result.Path) != backupDir || filepath.Ext(result.Path) != ".zip" {
		t.Fatalf("Path = %q, want generated ZIP under %q", result.Path, backupDir)
	}
	preview, err := InspectDataBackup(result.Path)
	if err != nil {
		t.Fatalf("InspectDataBackup() error = %v", err)
	}
	if preview.FileCount != result.FileCount || preview.UncompressedBytes != uint64(result.SourceBytes) ||
		!slices.Contains(preview.TopLevel, "saves") || !slices.Contains(preview.TopLevel, "settings.bin") {
		t.Fatalf("BackupPreview = %#v", preview)
	}

	names := backupTestZipNames(t, result.Path)
	for _, want := range []string{
		"saves/slot.msav", "mods/example.jar", "maps/island.msav", "settings.bin",
		"my-unknown-data/notes.txt", "my-unknown-data/cache/manual.txt", "empty-user-directory/",
	} {
		if !slices.Contains(names, want) {
			t.Errorf("ZIP missing %q; entries: %v", want, names)
		}
	}
	for _, unwanted := range []string{"cache/", "cache/textures.bin", "tmp/", "tmp/download.part", "outside-link"} {
		if slices.Contains(names, unwanted) {
			t.Errorf("ZIP unexpectedly contains %q", unwanted)
		}
	}
	if !backupTestWasSkipped(result, "cache", "tmp") {
		t.Fatalf("Skipped = %#v, want cache and tmp", result.Skipped)
	}
	if symlinkCreated && !backupTestWasSkipped(result, "outside-link") {
		t.Fatalf("Skipped = %#v, want outside-link", result.Skipped)
	}

	backups, err := ListBackups(backupDir)
	if err != nil {
		t.Fatalf("ListBackups() error = %v", err)
	}
	if len(backups) != 1 || backups[0].Path != result.Path || backups[0].Size != result.ArchiveBytes {
		t.Fatalf("ListBackups() = %#v, want created backup", backups)
	}
	if leftovers, err := filepath.Glob(filepath.Join(backupDir, ".backup-*.tmp")); err != nil || len(leftovers) != 0 {
		t.Fatalf("temporary files = %v, err = %v", leftovers, err)
	}
}

func TestCreateDataBackupDestinationInsideDataDirectoryDoesNotRecurse(t *testing.T) {
	dataDir := t.TempDir()
	writeBackupTestFile(t, filepath.Join(dataDir, "saves", "one.msav"), "save")
	backupDir := filepath.Join(dataDir, "backups")

	result, err := CreateDataBackup(dataDir, backupDir)
	if err != nil {
		t.Fatalf("CreateDataBackup() error = %v", err)
	}
	names := backupTestZipNames(t, result.Path)
	if !slices.Contains(names, "saves/one.msav") {
		t.Fatalf("ZIP entries = %v, want save", names)
	}
	for _, name := range names {
		if strings.HasPrefix(name, "backups/") {
			t.Fatalf("ZIP recursively contains output directory: %v", names)
		}
	}
}

func TestBackupAndRestoreCanonicalizeSymlinkedRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating directory symlinks commonly requires elevated privileges on Windows")
	}
	realRoot := t.TempDir()
	aliasRoot := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(realRoot, "data")
	writeBackupTestFile(t, filepath.Join(dataDir, "settings.bin"), "settings")
	result, err := CreateDataBackup(dataDir, filepath.Join(aliasRoot, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(result.Path, aliasRoot) {
		t.Fatalf("backup path was not canonicalized: %q", result.Path)
	}
	restoreAlias := filepath.Join(aliasRoot, "restored")
	if _, err := RestoreDataBackup(result.Path, restoreAlias); err != nil {
		t.Fatal(err)
	}
	if got := string(backupTestReadFile(t, filepath.Join(realRoot, "restored", "settings.bin"))); got != "settings" {
		t.Fatalf("restored settings = %q", got)
	}
}

func TestRestoreDataBackupRejectsZipSlip(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "malicious.zip")
	createBackupTestZip(t, archive, map[string]string{
		"safe/settings.bin": "safe",
		"../escaped.txt":    "escaped",
	})
	destination := filepath.Join(t.TempDir(), "game_data")
	escaped := filepath.Join(filepath.Dir(destination), "escaped.txt")

	_, err := RestoreDataBackup(archive, destination)
	if err == nil || !strings.Contains(err.Error(), "不安全") {
		t.Fatalf("RestoreDataBackup() error = %v, want unsafe-path error", err)
	}
	if _, err := os.Stat(escaped); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Zip Slip wrote %q, stat error = %v", escaped, err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed restore should not publish destination, stat error = %v", err)
	}
	if _, err := InspectDataBackup(archive); err == nil || !strings.Contains(err.Error(), "不安全") {
		t.Fatalf("InspectDataBackup() error = %v, want unsafe-path error", err)
	}
}

func TestRestoreDataBackupDoesNotOverwriteByDefault(t *testing.T) {
	source := t.TempDir()
	writeBackupTestFile(t, filepath.Join(source, "settings.bin"), "new settings")
	writeBackupTestFile(t, filepath.Join(source, "saves", "new.msav"), "new save")
	archive := filepath.Join(t.TempDir(), "backup.zip")
	if _, err := CreateDataBackup(source, archive); err != nil {
		t.Fatalf("CreateDataBackup() error = %v", err)
	}

	destination := t.TempDir()
	settingsPath := filepath.Join(destination, "settings.bin")
	writeBackupTestFile(t, settingsPath, "old settings")
	if _, err := RestoreDataBackup(archive, destination); err == nil || !strings.Contains(err.Error(), "Overwrite") {
		t.Fatalf("RestoreDataBackup() error = %v, want overwrite error", err)
	}
	if got := string(backupTestReadFile(t, settingsPath)); got != "old settings" {
		t.Fatalf("existing settings changed to %q after refused restore", got)
	}
	if _, err := os.Stat(filepath.Join(destination, "saves", "new.msav")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restore made partial changes before conflict check, stat error = %v", err)
	}

	result, err := RestoreDataBackup(archive, destination, RestoreOptions{Overwrite: true})
	if err != nil {
		t.Fatalf("RestoreDataBackup(overwrite) error = %v", err)
	}
	if result.FileCount != 2 || result.BytesWritten == 0 {
		t.Fatalf("RestoreResult = %#v", result)
	}
	if got := string(backupTestReadFile(t, settingsPath)); got != "new settings" {
		t.Fatalf("settings = %q after overwrite, want new settings", got)
	}
	if got := string(backupTestReadFile(t, filepath.Join(destination, "saves", "new.msav"))); got != "new save" {
		t.Fatalf("save = %q, want new save", got)
	}
}

func TestRestoreDataBackupRefusesFileDirectoryTypeReplacement(t *testing.T) {
	source := t.TempDir()
	writeBackupTestFile(t, filepath.Join(source, "mods", "example.jar"), "mod")
	archive := filepath.Join(t.TempDir(), "backup.zip")
	if _, err := CreateDataBackup(source, archive); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	writeBackupTestFile(t, filepath.Join(destination, "mods"), "must stay a file")
	if _, err := RestoreDataBackup(archive, destination, RestoreOptions{Overwrite: true}); err == nil || !strings.Contains(err.Error(), "目录") {
		t.Fatalf("type-conflict restore error = %v", err)
	}
	if got := string(backupTestReadFile(t, filepath.Join(destination, "mods"))); got != "must stay a file" {
		t.Fatalf("conflicting path changed to %q", got)
	}
}

func TestRestoreDataBackupRejectsCorruptZip(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "broken.zip")
	if err := os.WriteFile(archive, []byte("this is not a zip archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "game_data")

	_, err := RestoreDataBackup(archive, destination)
	if err == nil || !strings.Contains(err.Error(), "打开 ZIP") {
		t.Fatalf("RestoreDataBackup() error = %v, want corrupt ZIP error", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("corrupt ZIP published destination, stat error = %v", err)
	}
}

func TestRestoreDataBackupDetectsCorruptFileContentBeforePublishing(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "crc-error.zip")
	createBackupTestZip(t, archive, map[string]string{"settings.bin": strings.Repeat("abcdef", 100)})
	data := backupTestReadFile(t, archive)
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(reader.File) != 1 {
		t.Fatalf("opening fixture ZIP: files=%d err=%v", len(reader.File), err)
	}
	// Flip one byte in the compressed payload while leaving the central
	// directory intact, forcing decompression or CRC validation to fail.
	offset, err := reader.File[0].DataOffset()
	if err != nil {
		t.Fatal(err)
	}
	data[offset+1] ^= 0xff
	if err := os.WriteFile(archive, data, 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "game_data")

	_, err = RestoreDataBackup(archive, destination)
	if err == nil || !strings.Contains(err.Error(), "损坏") {
		t.Fatalf("RestoreDataBackup() error = %v, want content corruption error", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("corrupt content published destination, stat error = %v", err)
	}
}

func writeBackupTestFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func backupTestReadFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func backupTestZipNames(t *testing.T, name string) []string {
	t.Helper()
	reader, err := zip.OpenReader(name)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		names = append(names, file.Name)
	}
	return names
}

func backupTestWasSkipped(result BackupResult, paths ...string) bool {
	found := make(map[string]bool, len(result.Skipped))
	for _, item := range result.Skipped {
		found[item.Path] = true
	}
	for _, name := range paths {
		if !found[name] {
			return false
		}
	}
	return true
}

func createBackupTestZip(t *testing.T, name string, files map[string]string) {
	t.Helper()
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for fileName, content := range files {
		w, err := zw.Create(fileName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
