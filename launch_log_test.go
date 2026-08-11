package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunLaunchSessionCapturesOutput(t *testing.T) {
	if os.Getenv("GO_WANT_LAUNCH_HELPER") == "1" {
		_, _ = os.Stdout.WriteString("stdout marker\n")
		_, _ = os.Stderr.WriteString("stderr marker\n")
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestRunLaunchSessionCapturesOutput")
	cmd.Env = append(os.Environ(), "GO_WANT_LAUNCH_HELPER=1")
	spec := LaunchSpec{
		InstanceID: "server",
		Java:       JavaCandidate{Path: os.Args[0]},
		WorkingDir: t.TempDir(),
		Command:    cmd,
	}
	configDirectory := t.TempDir()
	session := newLaunchSession(spec, filepath.Join(configDirectory, "launcher.json"))
	msg := runLaunchSession(session)()
	finished, ok := msg.(launchFinishedMsg)
	if !ok {
		t.Fatalf("unexpected message: %T", msg)
	}
	if finished.err != nil {
		t.Fatal(finished.err)
	}
	if !strings.Contains(finished.output, "stdout marker") || !strings.Contains(finished.output, "stderr marker") {
		t.Fatalf("missing captured output: %q", finished.output)
	}
	if finished.logPath == "" {
		t.Fatal("expected persistent log path")
	}
	if got, want := filepath.Dir(finished.logPath), filepath.Join(configDirectory, "logs", "server"); got != want {
		t.Fatalf("log directory = %q, want %q", got, want)
	}
}

func TestCreateUniqueLaunchLogNeverTruncatesExistingLog(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 8, 11, 12, 34, 56, 789000000, time.UTC)
	first := createUniqueLaunchLog(directory, now)
	if first == nil {
		t.Fatal("first log was not created")
	}
	if _, err := first.WriteString("keep"); err != nil {
		t.Fatal(err)
	}
	firstPath := first.Name()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := createUniqueLaunchLog(directory, now)
	if second == nil {
		t.Fatal("second log was not created")
	}
	secondPath := second.Name()
	_ = second.Close()
	if firstPath == secondPath {
		t.Fatal("colliding log reused the existing path")
	}
	data, err := os.ReadFile(firstPath)
	if err != nil || string(data) != "keep" {
		t.Fatalf("existing log data = %q, err = %v", data, err)
	}
}

func TestNormalizeLogStripsTerminalControlSequencesForTUI(t *testing.T) {
	raw := "\x1b[2J\x1b[31merror\x1b[0m\a\b\r\nnext\rline\x7f"
	if got, want := normalizeLog(raw), "error\nnext\nline"; got != want {
		t.Fatalf("normalizeLog = %q, want %q", got, want)
	}
}

func TestLatestLaunchLogLoadsNewestInstanceTail(t *testing.T) {
	configDirectory := t.TempDir()
	configPath := filepath.Join(configDirectory, configFileName)
	logDirectory := filepath.Join(configDirectory, "logs", "server")
	if err := os.MkdirAll(logDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(logDirectory, "old.log")
	newPath := filepath.Join(logDirectory, "new.log")
	if err := os.WriteFile(oldPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("\x1b[31mnew\x1b[0m"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(oldPath, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, now, now); err != nil {
		t.Fatal(err)
	}
	path, output, err := latestLaunchLog(configPath, "server")
	if err != nil {
		t.Fatal(err)
	}
	if path != newPath || output != "\x1b[31mnew\x1b[0m" {
		t.Fatalf("latest path=%q output=%q", path, output)
	}

	launcher := defaultLauncherConfig()
	launcher.Instances[0].ID = "server"
	launcher.ActiveInstanceID = "server"
	m := newModel(launcher, configPath, "", false)
	if m.logPath != newPath || m.logText != "new" {
		t.Fatalf("model latest path=%q output=%q", m.logPath, m.logText)
	}
}
