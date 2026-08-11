package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckDirectoryWritableLeavesNoProbeFile(t *testing.T) {
	directory := t.TempDir()
	if err := checkDirectoryWritable(directory); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(directory, ".launcher-write-check-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("probe leftovers = %v, err = %v", matches, err)
	}
	file := filepath.Join(directory, "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkDirectoryWritable(file); err == nil {
		t.Fatal("write check unexpectedly accepted a file path")
	}
}

func TestTrialJVMArgumentsAcceptsValidAndRejectsInvalidFlags(t *testing.T) {
	javaPath, err := exec.LookPath("java")
	if err != nil {
		t.Skip("test environment has no Java")
	}
	workingDirectory := t.TempDir()
	valid := LaunchSpec{
		Java:       JavaCandidate{Path: javaPath},
		WorkingDir: workingDirectory,
		Args:       []string{"-Xms4g", "-Xmx4g", "-XX:+AlwaysPreTouch", "-XX:+UseG1GC", "-jar", "game.jar"},
	}
	if output, err := trialJVMArguments(valid); err != nil {
		t.Fatalf("valid trial failed: %v\n%s", err, output)
	}
	invalid := valid
	invalid.Args = []string{"-XX:DefinitelyNotARealOption=1", "-jar", "game.jar"}
	if output, err := trialJVMArguments(invalid); err == nil || !strings.Contains(output, "DefinitelyNotARealOption") {
		t.Fatalf("invalid trial output=%q err=%v", output, err)
	}
}

func TestClampPreflightOutput(t *testing.T) {
	if got := clampPreflightOutput("one\n two\tthree", 100); got != "one two three" {
		t.Fatalf("normalized = %q", got)
	}
	if got := clampPreflightOutput(strings.Repeat("x", 20), 5); got != "xxxxx…" {
		t.Fatalf("clamped = %q", got)
	}
}
