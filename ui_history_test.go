package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHistoryListsOpensDiagnosesAndDeletesInstanceLogs(t *testing.T) {
	configDirectory := t.TempDir()
	configPath := filepath.Join(configDirectory, configFileName)
	logDirectory := filepath.Join(configDirectory, "logs", defaultInstanceID)
	if err := os.MkdirAll(logDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(logDirectory, "old.log")
	failedPath := filepath.Join(logDirectory, "failed.log")
	if err := os.WriteFile(oldPath, []byte("normal exit"), 0o644); err != nil {
		t.Fatal(err)
	}
	failedLog := "Unrecognized VM option 'BadFlag'\n[启动器] 游戏进程异常结束: exit status 1\n"
	if err := os.WriteFile(failedPath, []byte(failedLog), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	_ = os.Chtimes(oldPath, now.Add(-time.Hour), now.Add(-time.Hour))
	_ = os.Chtimes(failedPath, now, now)

	m := newModel(defaultLauncherConfig(), configPath, "", false)
	m.showHistory = true
	m.refreshHistory()
	if len(m.historyLogs) != 2 || m.historyLogs[0].Path != failedPath {
		t.Fatalf("history = %#v", m.historyLogs)
	}
	opened, _ := m.openSelectedHistoryLog()
	m = opened.(model)
	if !m.showLog || m.showHistory || len(m.diagnostics) == 0 || m.diagnostics[0].Code != "unrecognized_jvm_option" {
		t.Fatalf("opened history diagnostics = %#v", m.diagnostics)
	}

	m.showLog = false
	m.showHistory = true
	m.refreshHistory()
	first, _ := m.updateHistory(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = first.(model)
	if !m.confirmDeleteLog {
		t.Fatal("history deletion did not require confirmation")
	}
	second, _ := m.updateHistory(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = second.(model)
	if _, err := os.Stat(failedPath); !os.IsNotExist(err) {
		t.Fatalf("failed log still exists: %v", err)
	}
	if len(m.historyLogs) != 1 || !strings.Contains(m.historyStatus, "已删除") {
		t.Fatalf("history after deletion = %#v, status=%q", m.historyLogs, m.historyStatus)
	}
}

func TestAnalyzeStoredLaunchLogDoesNotInventFailureForCleanExit(t *testing.T) {
	if diagnostics := AnalyzeStoredLaunchLog("[启动器] 游戏进程已退出。\n"); diagnostics != nil {
		t.Fatalf("clean history diagnostics = %#v", diagnostics)
	}
}

func TestHistoryCannotReplaceOrDeleteActiveLaunchLog(t *testing.T) {
	configDirectory := t.TempDir()
	logDirectory := filepath.Join(configDirectory, "logs", defaultInstanceID)
	if err := os.MkdirAll(logDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	activePath := filepath.Join(logDirectory, "active.log")
	oldPath := filepath.Join(logDirectory, "old.log")
	if err := os.WriteFile(activePath, []byte("active"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := &launchSession{writer: &launchLogWriter{file: mustOpenTestLog(t, activePath)}}
	defer session.writer.file.Close()
	m := newModel(defaultLauncherConfig(), filepath.Join(configDirectory, configFileName), "", false)
	m.launching = true
	m.activeSession = session
	m.logPath = activePath
	m.historyLogs = []LaunchLogInfo{{Path: oldPath, Name: "old.log"}}

	opened, _ := m.openSelectedHistoryLog()
	m = opened.(model)
	if m.activeSession != session || m.logPath != activePath || !strings.Contains(m.historyStatus, "运行期间") {
		t.Fatalf("active session changed: session=%p path=%q status=%q", m.activeSession, m.logPath, m.historyStatus)
	}
	deleted, _ := m.updateHistory(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = deleted.(model)
	if _, err := os.Stat(oldPath); err != nil || m.confirmDeleteLog {
		t.Fatalf("running history deletion changed file: err=%v confirm=%v", err, m.confirmDeleteLog)
	}
}

func TestHistoryCanDeleteClosedLatestSessionLog(t *testing.T) {
	configDirectory := t.TempDir()
	logDirectory := filepath.Join(configDirectory, "logs", defaultInstanceID)
	if err := os.MkdirAll(logDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	closedPath := filepath.Join(logDirectory, "closed.log")
	if err := os.WriteFile(closedPath, []byte("closed"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newModel(defaultLauncherConfig(), filepath.Join(configDirectory, configFileName), "", false)
	m.launching = false
	m.activeSession = &launchSession{writer: &launchLogWriter{}}
	m.historyLogs = []LaunchLogInfo{{Path: closedPath, Name: "closed.log"}}
	first, _ := m.updateHistory(keyRune('d'))
	m = first.(model)
	second, _ := m.updateHistory(keyRune('d'))
	m = second.(model)
	if _, err := os.Stat(closedPath); !os.IsNotExist(err) {
		t.Fatalf("closed latest log was not deleted: %v", err)
	}
}

func mustOpenTestLog(t *testing.T, path string) *os.File {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	return file
}
