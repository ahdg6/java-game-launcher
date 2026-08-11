package main

import (
	"path/filepath"
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
