package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIProcessPersistsOutputWithoutTakingOverStdin(t *testing.T) {
	if os.Getenv("GO_WANT_CLI_LOG_HELPER") == "1" {
		_, _ = os.Stdout.WriteString("cli stdout marker\n")
		_, _ = os.Stderr.WriteString("cli stderr marker\n")
		return
	}
	command := exec.Command(os.Args[0], "-test.run=TestRunCLIProcessPersistsOutputWithoutTakingOverStdin")
	command.Env = append(os.Environ(), "GO_WANT_CLI_LOG_HELPER=1")
	workingDirectory := t.TempDir()
	configDirectory := t.TempDir()
	spec := LaunchSpec{
		InstanceID: defaultInstanceID,
		Java:       JavaCandidate{Path: os.Args[0]},
		WorkingDir: workingDirectory,
		Command:    command,
	}
	if err := runCLIProcess(spec, filepath.Join(configDirectory, configFileName)); err != nil {
		t.Fatal(err)
	}
	logs, err := filepath.Glob(filepath.Join(configDirectory, "logs", defaultInstanceID, "*.log"))
	if err != nil || len(logs) != 1 {
		t.Fatalf("logs = %v, err = %v", logs, err)
	}
	data, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "cli stdout marker") || !strings.Contains(text, "cli stderr marker") {
		t.Fatalf("CLI log output = %q", text)
	}
}

func TestSaveCLIAutoSelectionsDoesNotChangeConfiguredActiveInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)
	launcher := defaultLauncherConfig()
	selected, err := launcher.CreateInstance("selected", "Selected")
	if err != nil {
		t.Fatal(err)
	}
	cfg := selected.Config()
	cfg.JavaPath = "runtimes/zulu/bin/java"
	cfg.JarPath = "Mindustry.jar"
	if err := saveCLIAutoSelections(path, &launcher, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadLauncherConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ActiveInstanceID != defaultInstanceID {
		t.Fatalf("CLI selection changed active instance to %q", loaded.ActiveInstanceID)
	}
	got := loaded.InstanceByID("selected")
	if got == nil || got.JavaPath != cfg.JavaPath || got.JarPath != cfg.JarPath {
		t.Fatalf("selected instance paths = %#v", got)
	}
}
