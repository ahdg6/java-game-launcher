package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRunCLIProcessPersistsOutputAndUsesInjectedStreams(t *testing.T) {
	if os.Getenv("GO_WANT_CLI_LOG_HELPER") == "1" {
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Fprintf(os.Stdout, "stdin: %s\n", input)
		_, _ = os.Stdout.WriteString("cli stdout marker\n")
		_, _ = os.Stderr.WriteString("cli stderr marker\n")
		return
	}
	command := exec.Command(os.Args[0], "-test.run=TestRunCLIProcessPersistsOutputAndUsesInjectedStreams")
	command.Env = append(os.Environ(), "GO_WANT_CLI_LOG_HELPER=1")
	workingDirectory := t.TempDir()
	configDirectory := t.TempDir()
	spec := LaunchSpec{
		InstanceID: defaultInstanceID,
		Java:       JavaCandidate{Path: os.Args[0]},
		WorkingDir: workingDirectory,
		Command:    command,
	}
	var stdout, stderr bytes.Buffer
	if err := runCLIProcess(spec, filepath.Join(configDirectory, configFileName), strings.NewReader("injected input"), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "cli stdout marker") || !strings.Contains(stderr.String(), "cli stderr marker") {
		t.Fatalf("process output was not forwarded: stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "stdin: injected input") {
		t.Fatalf("process did not receive injected stdin: %q", stdout.String())
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

func TestRunFlagErrorsAndHelpAreRepeatable(t *testing.T) {
	for _, test := range []struct {
		name       string
		args       []string
		wantStatus int
		wantOutput string
	}{
		{"help", []string{"--help"}, 0, "-- 游戏参数"},
		{"unknown flag", []string{"--unknown"}, 2, "flag provided but not defined"},
		{"missing value", []string{"--config"}, 2, "flag needs an argument"},
		{"invalid boolean", []string{"--launch=maybe"}, 2, "invalid boolean value"},
		{"help again", []string{"-h"}, 0, "-- 游戏参数"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if status := Run(test.args, strings.NewReader(""), &stdout, &stderr); status != test.wantStatus {
				t.Fatalf("status = %d, want %d; stderr: %s", status, test.wantStatus, &stderr)
			}
			if !strings.Contains(stderr.String(), test.wantOutput) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.wantOutput)
			}
			if stdout.Len() != 0 {
				t.Fatalf("unexpected stdout: %s", &stdout)
			}
		})
	}
}

func TestCLIOptionsPreserveGameArguments(t *testing.T) {
	var output bytes.Buffer
	options, err := parseCLIOptions([]string{"--config", "a config.json", "--instance", "server", "--dry-run", "--", "--launch", "argument with spaces", ""}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if options.configPath != "a config.json" || options.instance != "server" || !options.dryRun || options.launch {
		t.Fatalf("launcher options = %#v", options)
	}
	want := []string{"--launch", "argument with spaces", ""}
	if !reflect.DeepEqual(options.gameArgs, want) {
		t.Fatalf("game arguments = %#v, want %#v", options.gameArgs, want)
	}
}

func TestRunCLIFailsBeforeDiscoveryOnInvalidConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"--launch", "--dry-run", "--diagnose", "--preflight"} {
		t.Run(mode, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if status := Run([]string{"--config", path, mode}, strings.NewReader(""), &stdout, &stderr); status != 1 {
				t.Fatalf("status = %d; stderr: %s", status, &stderr)
			}
			if stderr.Len() == 0 || stdout.Len() != 0 {
				t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunCLIRejectsUnknownInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)
	if err := saveLauncherConfig(path, defaultLauncherConfig()); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if status := Run([]string{"--config", path, "--instance", "missing-instance", "--launch"}, strings.NewReader(""), &stdout, &stderr); status != 1 {
		t.Fatalf("status = %d; stderr: %s", status, &stderr)
	}
	if !strings.Contains(stderr.String(), "missing-instance") || stdout.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}
