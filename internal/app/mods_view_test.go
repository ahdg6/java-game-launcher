package app

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ahdg6/java-game-launcher/internal/mindustry"
)

func TestTUIBulkModRestoreWaitsForGameExit(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	modDir := filepath.Join(dataDir, "mods", "example")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modDir, "mod.json"), []byte(`{"name":"example"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	mods, err := mindustry.ScanMods(dataDir)
	if err != nil || len(mods) != 1 {
		t.Fatalf("scan fixture: mods=%#v err=%v", mods, err)
	}
	plan, err := mindustry.DisableAllMods(mods)
	if err != nil || len(plan.Changes) != 1 {
		t.Fatalf("disable fixture: plan=%#v err=%v", plan, err)
	}
	disabledPath := plan.Changes[0].After.Path
	m := newModel(defaultLauncherConfig(), filepath.Join(root, configFileName), "", false)
	m.cfg.DataDirectory = dataDir
	m.page, m.launching, m.modDisablePlan = pageMods, true, plan
	updated, command := m.Update(keyRune('u'))
	m = updated.(model)
	if command != nil || !m.modsStatusErr || !reflect.DeepEqual(m.modDisablePlan, plan) {
		t.Fatal("running restore did not preserve pending plan")
	}
	if _, err := os.Stat(disabledPath); err != nil {
		t.Fatalf("disabled mod was moved while running: %v", err)
	}
	if _, err := os.Stat(modDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mod was enabled while running: %v", err)
	}
	// The retained plan remains usable after exit.
	m.launching = false
	updated, _ = m.Update(keyRune('u'))
	m = updated.(model)
	if m.modsStatusErr || len(m.modDisablePlan.Changes) != 0 {
		t.Fatalf("restore after exit failed: %s", m.modsStatus)
	}
	if _, err := os.Stat(modDir); err != nil {
		t.Fatalf("mod not restored after exit: %v", err)
	}
}
