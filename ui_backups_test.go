package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTUIRestoreCreatesSafetyBackupBeforeOverwrite(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, configFileName)
	dataDir := filepath.Join(root, "current-data")
	writeBackupTestFile(t, filepath.Join(dataDir, "settings.bin"), "current settings")

	launcher := defaultLauncherConfig()
	launcher.Instances[0].DataDirectory = dataDir
	m := newModel(launcher, configPath, "", false)

	source := filepath.Join(root, "source")
	writeBackupTestFile(t, filepath.Join(source, "settings.bin"), "restored settings")
	writeBackupTestFile(t, filepath.Join(source, "saves", "slot.msav"), "save")
	if _, err := CreateDataBackup(source, m.backupDirectory()); err != nil {
		t.Fatal(err)
	}
	m.refreshBackups()
	if len(m.backups) != 1 || m.backupPreview.FileCount != 2 {
		t.Fatalf("backups=%#v preview=%#v", m.backups, m.backupPreview)
	}

	first, _ := m.restoreSelectedBackup()
	m = first.(model)
	if !m.confirmRestoreBackup {
		t.Fatal("first restore key did not require confirmation")
	}
	second, command := m.restoreSelectedBackup()
	m = second.(model)
	if command == nil || !m.backupBusy {
		t.Fatal("confirmed restore did not start")
	}
	message := command()
	updated, _ := m.updateBackups(message)
	m = updated.(model)
	if m.backupStatusErr || !strings.Contains(m.backupStatus, "恢复完成") {
		t.Fatalf("status=%q err=%v", m.backupStatus, m.backupStatusErr)
	}
	if got := string(backupTestReadFile(t, filepath.Join(dataDir, "settings.bin"))); got != "restored settings" {
		t.Fatalf("restored settings = %q", got)
	}
	if m.lastBackup.Path == "" {
		t.Fatal("safety backup was not recorded")
	}
	if _, err := os.Stat(m.lastBackup.Path); err != nil {
		t.Fatalf("safety backup missing: %v", err)
	}
	safetyDestination := filepath.Join(root, "safety-restore")
	if _, err := RestoreDataBackup(m.lastBackup.Path, safetyDestination); err != nil {
		t.Fatal(err)
	}
	if got := string(backupTestReadFile(t, filepath.Join(safetyDestination, "settings.bin"))); got != "current settings" {
		t.Fatalf("safety backup contains %q", got)
	}
}

func TestBackupDirectoryIsIsolatedByInstance(t *testing.T) {
	root := t.TempDir()
	launcher := defaultLauncherConfig()
	second, err := launcher.CreateInstance("server", "Server")
	if err != nil {
		t.Fatal(err)
	}
	launcher.ActiveInstanceID = second.ID
	m := newModel(launcher, filepath.Join(root, configFileName), "", false)
	if got, want := m.backupDirectory(), filepath.Join(root, "backups", "server"); got != want {
		t.Fatalf("backup directory = %q, want %q", got, want)
	}
}
