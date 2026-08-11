package main

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTUISwitchInstancePreservesUnsavedCurrentEdits(t *testing.T) {
	launcher := defaultLauncherConfig()
	launcher.Instances[0].JarPath = "first.jar"
	second, err := launcher.CreateInstance("second", "Second")
	if err != nil {
		t.Fatal(err)
	}
	second.JarPath = "second.jar"
	m := newModel(launcher, filepath.Join(t.TempDir(), configFileName), "", false)
	m.cfg.JarPath = "edited-first.jar"

	updated, _ := m.selectInstanceAt(1, false)
	got := updated.(model)
	if got.cfg.InstanceID != "second" || got.cfg.JarPath != "second.jar" {
		t.Fatalf("selected config = %#v", got.cfg)
	}
	if stored := got.launcher.InstanceByID(defaultInstanceID).JarPath; stored != "edited-first.jar" {
		t.Fatalf("unsaved current edit was lost: %q", stored)
	}
}

func TestTUIDeletingActiveInstanceDoesNotOverwriteReplacement(t *testing.T) {
	launcher := defaultLauncherConfig()
	launcher.Instances[0].JarPath = "first.jar"
	second, err := launcher.CreateInstance("second", "Second")
	if err != nil {
		t.Fatal(err)
	}
	second.JarPath = "second.jar"
	m := newModel(launcher, filepath.Join(t.TempDir(), configFileName), "", false)
	m.showInstances = true
	m.instancesCursor = 0

	first, _ := m.updateInstances(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	secondUpdate, _ := first.(model).updateInstances(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	got := secondUpdate.(model)
	if got.cfg.InstanceID != "second" || got.cfg.JarPath != "second.jar" {
		t.Fatalf("replacement instance was overwritten: %#v", got.cfg)
	}
	if len(got.launcher.Instances) != 1 || got.launcher.Instances[0].JarPath != "second.jar" {
		t.Fatalf("launcher = %#v", got.launcher)
	}
}

func TestTUIIgnoresLateDiscoveryFromPreviousInstance(t *testing.T) {
	launcher := defaultLauncherConfig()
	if _, err := launcher.CreateInstance("second", "Second"); err != nil {
		t.Fatal(err)
	}
	m := newModel(launcher, filepath.Join(t.TempDir(), configFileName), "", false)
	oldGeneration := m.discoveryGeneration
	updated, _ := m.selectInstanceAt(1, false)
	m = updated.(model)

	late := environmentMsg{
		instanceID: defaultInstanceID,
		generation: oldGeneration,
		env:        Environment{Java: []JavaCandidate{{Path: "stale-java"}}},
	}
	result, _ := m.Update(late)
	got := result.(model)
	if len(got.env.Java) != 0 || !got.loading {
		t.Fatalf("late discovery mutated active instance: %#v", got.env)
	}
}

func TestTUIReportsSharedDataDirectory(t *testing.T) {
	launcher := defaultLauncherConfig()
	second, err := launcher.CreateInstance("second", "Second")
	if err != nil {
		t.Fatal(err)
	}
	second.JarPath = launcher.Instances[0].JarPath
	second.DataDirectory = launcher.Instances[0].DataDirectory
	m := newModel(launcher, filepath.Join(t.TempDir(), configFileName), "", false)
	if names := m.sharedDataDirectoryNames(defaultInstanceID); len(names) != 1 || names[0] != "Second" {
		t.Fatalf("shared names = %#v", names)
	}
}

func TestInstanceIDGenerationIsStableAndPortable(t *testing.T) {
	launcher := defaultLauncherConfig()
	if got := instanceIDFromName("模组 测试"); got != "instance" {
		t.Fatalf("Chinese fallback ID = %q", got)
	}
	if got := instanceIDFromName("My Modded_Game"); got != "my-modded-game" {
		t.Fatalf("ASCII ID = %q", got)
	}
	first := uniqueInstanceID(launcher, defaultInstanceID)
	if first != "default-2" {
		t.Fatalf("unique ID = %q", first)
	}
}
