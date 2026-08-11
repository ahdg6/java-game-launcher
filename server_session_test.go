package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestServerSessionHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_SERVER_SESSION_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Printf("echo:%s\n", line)
		if line == "quit" {
			return
		}
	}
}

func TestLaunchSessionInputEchoAndExitErrors(t *testing.T) {
	session := newServerTestSession(t)
	result := runSessionAsync(session)
	stdin := waitForSessionStdin(t, session)

	if _, err := stdin.Write([]byte("direct\n")); err != nil {
		t.Fatalf("write through Stdin: %v", err)
	}

	const commands = 24
	var wg sync.WaitGroup
	errs := make(chan error, commands)
	for i := 0; i < commands; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- session.SendInput(fmt.Sprintf("command-%02d", i))
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent SendInput: %v", err)
		}
	}
	if err := session.SendInput("quit\n"); err != nil {
		t.Fatalf("send quit: %v", err)
	}

	finished := waitForFinished(t, result)
	if finished.err != nil {
		t.Fatalf("helper process failed: %v\n%s", finished.err, finished.output)
	}
	if !strings.Contains(finished.output, "echo:direct") {
		t.Fatalf("direct stdin was not echoed: %q", finished.output)
	}
	for i := 0; i < commands; i++ {
		marker := fmt.Sprintf("echo:command-%02d", i)
		if !strings.Contains(finished.output, marker) {
			t.Fatalf("missing %q in output", marker)
		}
	}
	if err := session.SendInput("after-exit"); !errors.Is(err, ErrLaunchProcessExited) {
		t.Fatalf("SendInput after exit = %v, want ErrLaunchProcessExited", err)
	}
	if err := session.Stop(); !errors.Is(err, ErrLaunchProcessExited) {
		t.Fatalf("Stop after exit = %v, want ErrLaunchProcessExited", err)
	}
	select {
	case <-session.Done():
	default:
		t.Fatal("Done was not closed after exit")
	}
}

func TestLaunchSessionStopRunningProcess(t *testing.T) {
	session := newServerTestSession(t)
	result := runSessionAsync(session)
	_ = waitForSessionStdin(t, session)

	if err := session.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	finished := waitForFinished(t, result)
	if !errors.Is(finished.err, ErrLaunchStopped) {
		t.Fatalf("finished error = %v, want ErrLaunchStopped", finished.err)
	}
	if err := session.SendInput("after-stop"); !errors.Is(err, ErrLaunchProcessExited) {
		t.Fatalf("SendInput after stop = %v, want ErrLaunchProcessExited", err)
	}
}

func TestLaunchSessionStopBeforeRun(t *testing.T) {
	session := newServerTestSession(t)
	if err := session.Stop(); err != nil {
		t.Fatalf("Stop before run: %v", err)
	}
	finished := runLaunchSession(session)().(launchFinishedMsg)
	if !errors.Is(finished.err, ErrLaunchStopped) {
		t.Fatalf("finished error = %v, want ErrLaunchStopped", finished.err)
	}
	if session.spec.Command.Process != nil {
		t.Fatal("process unexpectedly started after pending launch was stopped")
	}
}

func TestLaunchSessionStopsWhenStartedHookCannotPersistState(t *testing.T) {
	session := newServerTestSession(t)
	session.onStarted = func(pid int) error {
		if pid <= 0 {
			return fmt.Errorf("invalid started hook PID %d", pid)
		}
		return errors.New("marker update failed")
	}
	finished := waitForFinished(t, runSessionAsync(session))
	if finished.err == nil || !strings.Contains(finished.err.Error(), "marker update failed") {
		t.Fatalf("finished error = %v", finished.err)
	}
	if session.spec.Command.ProcessState == nil {
		t.Fatalf("process was not reaped after hook failure: %#v", session.spec.Command.ProcessState)
	}
	select {
	case <-session.Done():
	default:
		t.Fatal("Done was not closed after hook failure")
	}
}

func TestLaunchSessionStopRacingWithStart(t *testing.T) {
	for i := 0; i < 20; i++ {
		session := newServerTestSession(t)
		result := runSessionAsync(session)
		stopResult := make(chan error, 1)
		go func() { stopResult <- session.Stop() }()
		if err := <-stopResult; err != nil {
			t.Fatalf("iteration %d Stop: %v", i, err)
		}
		finished := waitForFinished(t, result)
		if !errors.Is(finished.err, ErrLaunchStopped) {
			t.Fatalf("iteration %d finished error = %v, want ErrLaunchStopped", i, finished.err)
		}
	}
}

func newServerTestSession(t *testing.T) *launchSession {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestServerSessionHelperProcess$")
	cmd.Env = append(os.Environ(), "GO_WANT_SERVER_SESSION_HELPER=1")
	spec := LaunchSpec{
		Java:       JavaCandidate{Path: os.Args[0]},
		WorkingDir: t.TempDir(),
		Command:    cmd,
	}
	return newLaunchSession(spec, t.TempDir()+"/launcher.json")
}

func runSessionAsync(session *launchSession) <-chan launchFinishedMsg {
	result := make(chan launchFinishedMsg, 1)
	go func() {
		result <- runLaunchSession(session)().(launchFinishedMsg)
	}()
	return result
}

func waitForSessionStdin(t *testing.T, session *launchSession) *launchSessionInput {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stdin, err := session.Stdin()
		if err == nil {
			input, ok := stdin.(*launchSessionInput)
			if !ok {
				t.Fatalf("Stdin returned %T, want *launchSessionInput", stdin)
			}
			return input
		}
		if !errors.Is(err, ErrLaunchNotStarted) {
			t.Fatalf("wait for stdin: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for session stdin")
	return nil
}

func waitForFinished(t *testing.T, result <-chan launchFinishedMsg) launchFinishedMsg {
	t.Helper()
	select {
	case finished := <-result:
		return finished
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for launch session")
		return launchFinishedMsg{}
	}
}
