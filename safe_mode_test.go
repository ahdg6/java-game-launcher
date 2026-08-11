package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMindustrySafeModeBeginEndRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	stateDir := t.TempDir()
	modsDir := filepath.Join(dataDir, "mods")
	writeModTestFile(t, filepath.Join(modsDir, "alpha.jar"), "alpha")
	writeModTestFile(t, filepath.Join(modsDir, "folder", "mod.json"), `{"name":"folder"}`)
	writeModTestFile(t, filepath.Join(modsDir, "manual.zip.disabled"), "manual")

	if err := BeginMindustrySafeMode(dataDir, stateDir); err != nil {
		t.Fatalf("BeginMindustrySafeMode() error = %v", err)
	}
	pending, err := IsMindustrySafeModePending(stateDir)
	if err != nil || !pending {
		t.Fatalf("IsMindustrySafeModePending() = %v, %v; want true, nil", pending, err)
	}
	for _, name := range []string{"alpha.jar.disabled", "folder.disabled", "manual.zip.disabled"} {
		if _, err := os.Lstat(filepath.Join(modsDir, name)); err != nil {
			t.Errorf("disabled mod %q missing: %v", name, err)
		}
	}
	for _, name := range []string{"alpha.jar", "folder"} {
		if _, err := os.Lstat(filepath.Join(modsDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("enabled mod %q remains: %v", name, err)
		}
	}

	marker := readSafeModeTestMarker(t, stateDir)
	if len(marker.Changes) != 2 {
		t.Fatalf("marker changes = %#v, want 2", marker.Changes)
	}
	for _, change := range marker.Changes {
		if !filepath.IsAbs(change.Before) || change.After != change.Before+".disabled" {
			t.Errorf("unsafe marker change = %#v", change)
		}
	}

	if err := EndMindustrySafeMode(dataDir, stateDir); err != nil {
		t.Fatalf("EndMindustrySafeMode() error = %v", err)
	}
	for _, name := range []string{"alpha.jar", "folder", "manual.zip.disabled"} {
		if _, err := os.Lstat(filepath.Join(modsDir, name)); err != nil {
			t.Errorf("restored mod %q missing: %v", name, err)
		}
	}
	if got := string(readModTestFile(t, filepath.Join(modsDir, "manual.zip.disabled"))); got != "manual" {
		t.Fatalf("already-disabled mod changed to %q", got)
	}
	pending, err = IsMindustrySafeModePending(stateDir)
	if err != nil || pending {
		t.Fatalf("pending after End = %v, %v; want false, nil", pending, err)
	}
}

func TestMindustrySafeModeNoMods(t *testing.T) {
	dataDir := t.TempDir()
	stateDir := t.TempDir()

	if err := BeginMindustrySafeMode(dataDir, stateDir); err != nil {
		t.Fatal(err)
	}
	marker := readSafeModeTestMarker(t, stateDir)
	if marker.Changes == nil || len(marker.Changes) != 0 {
		t.Fatalf("empty marker changes = %#v, want non-nil empty", marker.Changes)
	}
	if err := EndMindustrySafeMode(dataDir, stateDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "mods")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("safe mode unexpectedly created mods directory: %v", err)
	}
}

func TestSafeModeDoesNotRecoverWhileBoundGameProcessRuns(t *testing.T) {
	if os.Getenv("GO_WANT_SAFE_MODE_OWNER_HELPER") == "1" {
		time.Sleep(30 * time.Second)
		return
	}
	dataDir := t.TempDir()
	stateDir := t.TempDir()
	writeModTestFile(t, filepath.Join(dataDir, "mods", "safe.jar"), "safe")
	if err := BeginMindustrySafeMode(dataDir, stateDir); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=TestSafeModeDoesNotRecoverWhileBoundGameProcessRuns")
	command.Env = append(os.Environ(), "GO_WANT_SAFE_MODE_OWNER_HELPER=1")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	}()
	if err := SetMindustrySafeModeProcess(dataDir, stateDir, command.Process.Pid); err != nil {
		t.Fatal(err)
	}
	marker := readSafeModeTestMarker(t, stateDir)
	if marker.ProcessPID != command.Process.Pid || marker.OwnerPID != os.Getpid() {
		t.Fatalf("marker process binding = %#v", marker)
	}
	recovered, err := RecoverInterruptedSafeMode(dataDir, stateDir)
	if err == nil || !recovered || !strings.Contains(err.Error(), "运行中") {
		t.Fatalf("live-process recovery = %v, %v", recovered, err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "mods", "safe.jar.disabled")); err != nil {
		t.Fatalf("mod was restored under a running game: %v", err)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("killed helper unexpectedly exited cleanly")
	}
	command.Process = nil
	recovered, err = RecoverInterruptedSafeMode(dataDir, stateDir)
	if err != nil || !recovered {
		t.Fatalf("dead-process recovery = %v, %v", recovered, err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "mods", "safe.jar")); err != nil {
		t.Fatalf("mod was not restored after process exit: %v", err)
	}
}

func TestRecoverInterruptedMindustrySafeModeDisable(t *testing.T) {
	dataDir := t.TempDir()
	stateDir := t.TempDir()
	modsDir := filepath.Join(dataDir, "mods")
	writeModTestFile(t, filepath.Join(modsDir, "alpha.jar"), "alpha")
	writeModTestFile(t, filepath.Join(modsDir, "beta.jar"), "beta")

	marker := prepareSafeModeTestMarker(t, dataDir, stateDir)
	if err := os.Rename(marker.Changes[0].Before, marker.Changes[0].After); err != nil {
		t.Fatal(err)
	}

	recovered, err := RecoverInterruptedSafeMode(dataDir, stateDir)
	if err != nil || !recovered {
		t.Fatalf("RecoverInterruptedSafeMode() = %v, %v; want true, nil", recovered, err)
	}
	for _, name := range []string{"alpha.jar", "beta.jar"} {
		if _, err := os.Lstat(filepath.Join(modsDir, name)); err != nil {
			t.Errorf("%q was not restored: %v", name, err)
		}
		if _, err := os.Lstat(filepath.Join(modsDir, name+".disabled")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("disabled path for %q remains: %v", name, err)
		}
	}
}

func TestRecoverInterruptedMindustrySafeModeRestore(t *testing.T) {
	dataDir := t.TempDir()
	stateDir := t.TempDir()
	modsDir := filepath.Join(dataDir, "mods")
	writeModTestFile(t, filepath.Join(modsDir, "alpha.jar"), "alpha")
	writeModTestFile(t, filepath.Join(modsDir, "beta.jar"), "beta")
	if err := BeginMindustrySafeMode(dataDir, stateDir); err != nil {
		t.Fatal(err)
	}
	marker := readSafeModeTestMarker(t, stateDir)
	if err := os.Rename(marker.Changes[0].After, marker.Changes[0].Before); err != nil {
		t.Fatal(err)
	}

	recovered, err := RecoverInterruptedSafeMode(dataDir, stateDir)
	if err != nil || !recovered {
		t.Fatalf("RecoverInterruptedSafeMode() = %v, %v; want true, nil", recovered, err)
	}
	for _, name := range []string{"alpha.jar", "beta.jar"} {
		if _, err := os.Lstat(filepath.Join(modsDir, name)); err != nil {
			t.Errorf("%q was not restored: %v", name, err)
		}
	}
}

func TestMindustrySafeModeBeginResumesInterruptedDisable(t *testing.T) {
	dataDir := t.TempDir()
	stateDir := t.TempDir()
	modsDir := filepath.Join(dataDir, "mods")
	writeModTestFile(t, filepath.Join(modsDir, "alpha.jar"), "alpha")
	writeModTestFile(t, filepath.Join(modsDir, "beta.jar"), "beta")
	marker := prepareSafeModeTestMarker(t, dataDir, stateDir)
	if err := os.Rename(marker.Changes[0].Before, marker.Changes[0].After); err != nil {
		t.Fatal(err)
	}

	if err := BeginMindustrySafeMode(dataDir, stateDir); err != nil {
		t.Fatalf("repeated Begin error = %v", err)
	}
	for _, change := range marker.Changes {
		if _, err := os.Lstat(change.After); err != nil {
			t.Errorf("pending disable %q not completed: %v", change.After, err)
		}
	}
	if err := EndMindustrySafeMode(dataDir, stateDir); err != nil {
		t.Fatal(err)
	}
}

func TestMindustrySafeModeRestoreConflictPreservesEverything(t *testing.T) {
	dataDir := t.TempDir()
	stateDir := t.TempDir()
	modsDir := filepath.Join(dataDir, "mods")
	writeModTestFile(t, filepath.Join(modsDir, "alpha.jar"), "alpha")
	writeModTestFile(t, filepath.Join(modsDir, "beta.jar"), "beta")
	if err := BeginMindustrySafeMode(dataDir, stateDir); err != nil {
		t.Fatal(err)
	}
	writeModTestFile(t, filepath.Join(modsDir, "alpha.jar"), "conflict")

	if err := EndMindustrySafeMode(dataDir, stateDir); err == nil || !strings.Contains(err.Error(), "冲突") {
		t.Fatalf("EndMindustrySafeMode() error = %v, want conflict", err)
	}
	for _, name := range []string{"alpha.jar.disabled", "beta.jar.disabled"} {
		if _, err := os.Lstat(filepath.Join(modsDir, name)); err != nil {
			t.Errorf("%q changed despite preflight conflict: %v", name, err)
		}
	}
	if got := string(readModTestFile(t, filepath.Join(modsDir, "alpha.jar"))); got != "conflict" {
		t.Fatalf("conflicting file changed to %q", got)
	}
	pending, err := IsMindustrySafeModePending(stateDir)
	if err != nil || !pending {
		t.Fatalf("pending after conflict = %v, %v", pending, err)
	}

	if err := os.Remove(filepath.Join(modsDir, "alpha.jar")); err != nil {
		t.Fatal(err)
	}
	if err := EndMindustrySafeMode(dataDir, stateDir); err != nil {
		t.Fatalf("retry End error = %v", err)
	}
}

func TestMindustrySafeModeRejectsCorruptAndEscapedMarker(t *testing.T) {
	t.Run("checksum", func(t *testing.T) {
		dataDir := t.TempDir()
		stateDir := t.TempDir()
		writeModTestFile(t, filepath.Join(dataDir, "mods", "safe.jar"), "safe")
		if err := BeginMindustrySafeMode(dataDir, stateDir); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(stateDir, mindustrySafeModeStateFile)
		data := readModTestFile(t, path)
		data = []byte(strings.Replace(string(data), "safe.jar", "evil.jar", 1))
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := EndMindustrySafeMode(dataDir, stateDir); err == nil || !strings.Contains(err.Error(), "校验") {
			t.Fatalf("tampered marker error = %v", err)
		}
		if _, err := os.Lstat(filepath.Join(dataDir, "mods", "safe.jar.disabled")); err != nil {
			t.Fatalf("mod changed after marker rejection: %v", err)
		}
	})

	t.Run("recomputed escape", func(t *testing.T) {
		dataDir := t.TempDir()
		stateDir := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside.jar")
		writeModTestFile(t, filepath.Join(dataDir, "mods", "safe.jar"), "safe")
		writeModTestFile(t, outside, "outside")
		if err := BeginMindustrySafeMode(dataDir, stateDir); err != nil {
			t.Fatal(err)
		}
		marker := readSafeModeTestMarker(t, stateDir)
		marker.Changes[0].Before = outside
		marker.Changes[0].After = outside + ".disabled"
		marker.Checksum = mindustrySafeModeChecksum(marker)
		writeSafeModeTestMarker(t, stateDir, marker)

		if err := EndMindustrySafeMode(dataDir, stateDir); err == nil || !strings.Contains(err.Error(), "越界") {
			t.Fatalf("escaped marker error = %v", err)
		}
		if got := string(readModTestFile(t, outside)); got != "outside" {
			t.Fatalf("outside file changed to %q", got)
		}
	})
}

func TestMindustrySafeModeRejectsWrongInstance(t *testing.T) {
	dataDir := t.TempDir()
	otherDataDir := t.TempDir()
	stateDir := t.TempDir()
	writeModTestFile(t, filepath.Join(dataDir, "mods", "safe.jar"), "safe")
	writeModTestFile(t, filepath.Join(otherDataDir, "mods", "other.jar"), "other")
	if err := BeginMindustrySafeMode(dataDir, stateDir); err != nil {
		t.Fatal(err)
	}

	recovered, err := RecoverInterruptedSafeMode(otherDataDir, stateDir)
	if err == nil || !recovered || !strings.Contains(err.Error(), "另一游戏实例") {
		t.Fatalf("wrong-instance recovery = %v, %v", recovered, err)
	}
	if got := string(readModTestFile(t, filepath.Join(otherDataDir, "mods", "other.jar"))); got != "other" {
		t.Fatalf("other instance changed to %q", got)
	}
	if err := EndMindustrySafeMode(dataDir, stateDir); err != nil {
		t.Fatal(err)
	}
}

func TestMindustrySafeModeMissingPathKeepsMarker(t *testing.T) {
	dataDir := t.TempDir()
	stateDir := t.TempDir()
	writeModTestFile(t, filepath.Join(dataDir, "mods", "lost.jar"), "lost")
	if err := BeginMindustrySafeMode(dataDir, stateDir); err != nil {
		t.Fatal(err)
	}
	marker := readSafeModeTestMarker(t, stateDir)
	if err := os.Remove(marker.Changes[0].After); err != nil {
		t.Fatal(err)
	}

	if err := EndMindustrySafeMode(dataDir, stateDir); err == nil || !strings.Contains(err.Error(), "均不存在") {
		t.Fatalf("missing paths error = %v", err)
	}
	pending, err := IsMindustrySafeModePending(stateDir)
	if err != nil || !pending {
		t.Fatalf("pending after missing mod = %v, %v", pending, err)
	}
}

func TestMindustrySafeModeIdempotent(t *testing.T) {
	dataDir := t.TempDir()
	stateDir := t.TempDir()
	writeModTestFile(t, filepath.Join(dataDir, "mods", "safe.jar"), "safe")

	if err := BeginMindustrySafeMode(dataDir, stateDir); err != nil {
		t.Fatal(err)
	}
	if err := BeginMindustrySafeMode(dataDir, stateDir); err != nil {
		t.Fatalf("second Begin error = %v", err)
	}
	if err := EndMindustrySafeMode(dataDir, stateDir); err != nil {
		t.Fatal(err)
	}
	if err := EndMindustrySafeMode(dataDir, stateDir); err != nil {
		t.Fatalf("second End error = %v", err)
	}
	recovered, err := RecoverInterruptedSafeMode(dataDir, stateDir)
	if err != nil || recovered {
		t.Fatalf("recovery without marker = %v, %v; want false, nil", recovered, err)
	}
}

func TestMindustrySafeModeRejectsSymlinkMarker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks commonly requires elevated privileges on Windows")
	}
	stateDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	writeModTestFile(t, outside, `{}`)
	if err := os.Symlink(outside, filepath.Join(stateDir, mindustrySafeModeStateFile)); err != nil {
		t.Fatal(err)
	}
	pending, err := IsMindustrySafeModePending(stateDir)
	if err == nil || !pending || !strings.Contains(err.Error(), "普通文件") {
		t.Fatalf("symlink marker result = %v, %v", pending, err)
	}
}

func prepareSafeModeTestMarker(t *testing.T, dataDir, stateDir string) mindustrySafeModeMarker {
	t.Helper()
	dataRoot, modsRoot, err := mindustrySafeModeRoots(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	mods, err := ScanMindustryMods(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanDisableAllMindustryMods(mods)
	if err != nil {
		t.Fatal(err)
	}
	marker := markerFromMindustrySafeModePlan(dataRoot, modsRoot, plan)
	stateRoot, _, err := mindustrySafeModeStateRoot(stateDir, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeMindustrySafeModeMarker(stateRoot, marker); err != nil {
		t.Fatal(err)
	}
	return marker
}

func readSafeModeTestMarker(t *testing.T, stateDir string) mindustrySafeModeMarker {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(stateDir, mindustrySafeModeStateFile))
	if err != nil {
		t.Fatal(err)
	}
	var marker mindustrySafeModeMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatal(err)
	}
	return marker
}

func writeSafeModeTestMarker(t *testing.T, stateDir string, marker mindustrySafeModeMarker) {
	t.Helper()
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(stateDir, mindustrySafeModeStateFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
