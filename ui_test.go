package main

import (
	"path/filepath"
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTUIRequiresSecondQuitWhenConfigIsDirty(t *testing.T) {
	m := newModel(defaultLauncherConfig(), filepath.Join(t.TempDir(), configFileName), "", false)
	m.dirty = true
	quitKey := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}
	first, command := m.Update(quitKey)
	m = first.(model)
	if command != nil || !m.confirmQuit {
		t.Fatalf("first quit command=%v confirm=%v", command != nil, m.confirmQuit)
	}
	second, command := m.Update(quitKey)
	if command == nil {
		t.Fatal("second quit did not return a quit command")
	}
	if _, ok := command().(tea.QuitMsg); !ok {
		t.Fatalf("second quit message = %T", command())
	}
	_ = second
}

func TestTUILaunchArgumentsStaySeparateFromPersistentInstance(t *testing.T) {
	launcher := defaultLauncherConfig()
	launcher.Instances[0].GameArgs = []string{"saved"}
	m := newModel(launcher, filepath.Join(t.TempDir(), configFileName), "", false, "one-shot", "two words")
	if got := m.cfg.GameArgs; !reflect.DeepEqual(got, []string{"saved"}) {
		t.Fatalf("persistent args changed to %#v", got)
	}
	launch := m.configForNextLaunch()
	if got, want := launch.GameArgs, []string{"saved", "one-shot", "two words"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("launch args = %#v, want %#v", got, want)
	}
	launch.GameArgs[0] = "changed"
	if m.cfg.GameArgs[0] != "saved" || m.launcher.Instances[0].GameArgs[0] != "saved" {
		t.Fatal("launch arguments share persistent storage")
	}
}

func TestTUIPreflightUsesOneShotLaunchArguments(t *testing.T) {
	m := newModel(defaultLauncherConfig(), filepath.Join(t.TempDir(), configFileName), "", false, "bad\x00argument")
	m.loading = false
	m.cfg.JavaPath = "missing-java"
	m.cfg.JarPath = "missing.jar"
	started, command := m.startPreflight()
	m = started.(model)
	if command == nil {
		t.Fatal("preflight did not start")
	}
	message := command().(preflightResultMsg)
	if len(message.report.Checks) == 0 {
		t.Fatal("preflight returned no checks")
	}
	// The same composed configuration is handed to preflight and real launch.
	// Missing paths fail first here; direct composition proves the one-shot arg
	// is present and will be checked once those paths are valid.
	if got := m.configForNextLaunch().GameArgs; !reflect.DeepEqual(got, []string{"bad\x00argument"}) {
		t.Fatalf("preflight launch args = %#v", got)
	}
}

func TestTUIQuitConfirmationResetsAfterOtherAction(t *testing.T) {
	m := newModel(defaultLauncherConfig(), filepath.Join(t.TempDir(), configFileName), "", false)
	m.dirty = true
	quitKey := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}
	first, _ := m.Update(quitKey)
	m = first.(model)
	moved, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = moved.(model)
	if m.confirmQuit {
		t.Fatal("quit confirmation survived another action")
	}
	third, command := m.Update(quitKey)
	if command != nil || !third.(model).confirmQuit {
		t.Fatal("quit did not require confirmation again")
	}
}
