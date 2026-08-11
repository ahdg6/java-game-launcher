package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
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
		Java:       JavaCandidate{Path: os.Args[0]},
		WorkingDir: t.TempDir(),
		Command:    cmd,
	}
	session := newLaunchSession(spec, t.TempDir()+"/launcher.json")
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
}
