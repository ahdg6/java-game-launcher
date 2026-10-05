package app

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahdg6/java-game-launcher/internal/java"
	tea "github.com/charmbracelet/bubbletea"
)

func TestLaunchPreparationIsDeferredAndLocksConfiguration(t *testing.T) {
	m := newModel(defaultLauncherConfig(), filepath.Join(t.TempDir(), configFileName), "", false)
	m.loading = false
	m.cfg.GameArgs = []string{"saved"}
	started, cmd := m.startConfiguredGame()
	m = started.(model)
	if cmd == nil || m.preparing == nil || m.launching {
		t.Fatal("launch did not enter preparation")
	}
	if _, err := os.Stat(m.cfgPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("configuration saved on event loop: %v", err)
	}
	request := m.preparing
	for _, key := range []tea.KeyMsg{keyRune('q'), keyRune('s'), keyRune('r'), {Type: tea.KeyEsc}, {Type: tea.KeyCtrlC}, {Type: tea.KeyEnter}} {
		updated, action := m.Update(key)
		m = updated.(model)
		if action != nil || m.preparing != request {
			t.Fatalf("key %q interrupted preparation", key.String())
		}
	}
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = resized.(model)
	if m.width != 100 {
		t.Fatal("preparation blocked window resize")
	}
	// Even direct mutation of the UI after scheduling cannot alter the saved snapshot.
	m.cfg.GameArgs[0] = "mutated"
	m.launcher.Instances[0].GameArgs[0] = "mutated-launcher"
	result := cmd().(launchPreparedMsg)
	saved, err := loadLauncherConfig(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.Instances[0].GameArgs[0]; got != "saved" {
		t.Fatalf("saved mutable snapshot: %q", got)
	}
	updated, _ := m.Update(result)
	m = updated.(model)
	if m.preparing != nil || m.launchErr == nil || m.page != pageLog {
		t.Fatal("failure did not complete preparation")
	}
}

func TestStaleLaunchMessagesCannotReplaceCurrentSession(t *testing.T) {
	m := newModel(defaultLauncherConfig(), filepath.Join(t.TempDir(), configFileName), "", false)
	request := &launchPreparation{instanceID: m.cfg.InstanceID}
	m.preparing = request
	stale := launchPreparedMsg{request: &launchPreparation{instanceID: m.cfg.InstanceID}, err: errors.New("old failure"), saved: true}
	updated, cmd := m.Update(stale)
	m = updated.(model)
	if cmd != nil || m.preparing != request || m.launchErr != nil {
		t.Fatal("stale preparation changed active request")
	}
	m.preparing = nil
	current := &launchSession{}
	m.activeSession, m.launching, m.logText = current, true, "current log"
	updated, cmd = m.Update(launchFinishedMsg{session: &launchSession{}, output: "old log"})
	m = updated.(model)
	if cmd != nil || !m.launching || m.activeSession != current || m.logText != "current log" {
		t.Fatal("stale completion replaced active session")
	}
}

func TestLaunchCompletionPreservesEditorAndBackgroundResults(t *testing.T) {
	m := newModel(defaultLauncherConfig(), filepath.Join(t.TempDir(), configFileName), "", false)
	m.loading = false
	session := &launchSession{}
	m.activeSession, m.launching = session, true
	m.beginPathEditor(editJavaPath, "Java", "old-java")
	updated, _ := m.Update(launchFinishedMsg{session: session, output: "done"})
	m = updated.(model)
	if m.page != pageMain || m.mode != editJavaPath {
		t.Fatal("completion interrupted editor")
	}
	m.input.SetValue("new-java")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.cfg.JavaPath != "new-java" || m.mode != editNone {
		t.Fatal("editor lost keyboard routing")
	}

	// Completion delivery is independent of the selected page.
	m.page, m.zuluBusy = pageLog, true
	installed := filepath.Join(t.TempDir(), "bin", javaExecutableName())
	updated, cmd := m.Update(zuluInstallMsg{result: java.ZuluInstallResult{JavaPath: installed}})
	m = updated.(model)
	if m.zuluBusy || cmd == nil || m.cfg.JavaPath != portablePath(m.cfgPath, installed) {
		t.Fatal("background install result lost outside runtime page")
	}
	m.toolBusy = true
	updated, _ = m.Update(toolResultMsg{message: "opened"})
	m = updated.(model)
	if m.toolBusy || m.toolStatus != "opened" {
		t.Fatal("background tool result lost outside tools page")
	}
}

func TestNestedToolsPagesReturnToTools(t *testing.T) {
	for _, selected := range []page{pageBackups, pageMods} {
		m := newModel(defaultLauncherConfig(), filepath.Join(t.TempDir(), configFileName), "", false)
		m.page = selected
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		m = updated.(model)
		if m.page != pageTools {
			t.Fatalf("page %d returned to %d", selected, m.page)
		}
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		if updated.(model).page != pageMain {
			t.Fatal("tools did not return to main")
		}
	}
}

func TestSessionCleansUpFailedProcessStartAndPersistsCleanupError(t *testing.T) {
	root := t.TempDir()
	session := newLaunchSession(LaunchSpec{Command: exec.Command(filepath.Join(root, "missing-java"))}, filepath.Join(root, configFileName))
	calls := 0
	cleanupErr := errors.New("restore failed")
	session.beforeStart = func() error { calls++; return nil }
	session.afterExit = func() error { calls++; return cleanupErr }
	result := runLaunchSession(session)().(launchFinishedMsg)
	if result.session != session || result.err == nil || !errors.Is(result.cleanupErr, cleanupErr) || calls != 2 {
		t.Fatalf("result=%#v calls=%d", result, calls)
	}
	log, err := os.ReadFile(result.logPath)
	if err != nil || !strings.Contains(string(log), "restore failed") {
		t.Fatalf("cleanup failure not persisted: %v %q", err, log)
	}
}

func TestSessionDoesNotCleanUpWhenSafeModePreparationFails(t *testing.T) {
	root := t.TempDir()
	session := newLaunchSession(LaunchSpec{Command: exec.Command(filepath.Join(root, "missing-java"))}, filepath.Join(root, configFileName))
	preparationErr := errors.New("safe mode owned by another process")
	session.beforeStart = func() error { return preparationErr }
	session.afterExit = func() error { t.Error("failed preparation restored an unrelated transaction"); return nil }
	result := runLaunchSession(session)().(launchFinishedMsg)
	if !errors.Is(result.err, preparationErr) || result.cleanupErr != nil {
		t.Fatalf("unexpected result: %#v", result)
	}
}
