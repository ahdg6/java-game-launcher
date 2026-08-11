package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

var (
	// ErrLaunchNotStarted indicates that the process has not reached the point
	// where its standard input is available yet.
	ErrLaunchNotStarted = errors.New("launch process has not started")
	// ErrLaunchProcessExited indicates that the process and its input pipe are
	// no longer available.
	ErrLaunchProcessExited = errors.New("launch process has exited")
	// ErrLaunchStopped is returned by the launch command after Stop cancels a
	// pending launch or terminates a running process.
	ErrLaunchStopped = errors.New("launch process was stopped")
)

type launchProcessState uint8

const (
	launchProcessPending launchProcessState = iota
	launchProcessStarting
	launchProcessRunning
	launchProcessExited
)

// launchProcessSession owns the pieces of exec.Cmd that must be coordinated
// with a TUI: stdin, Process.Kill, and Wait. stateMu never protects a blocking
// pipe write; inputMu serializes writes and closing the pipe independently.
type launchProcessSession struct {
	stateMu sync.Mutex
	state   launchProcessState
	process *os.Process

	stopRequested bool
	stopIssued    bool
	done          chan struct{}
	doneOnce      sync.Once

	inputMu sync.Mutex
	stdin   io.WriteCloser
	input   launchSessionInput
}

type launchSessionInput struct {
	process *launchProcessSession
}

func newLaunchProcessSession() *launchProcessSession {
	process := &launchProcessSession{done: make(chan struct{})}
	process.input.process = process
	return process
}

// Stdin returns a concurrency-safe handle to the running process's standard
// input. The handle remains safe to use after process exit; later operations
// then return ErrLaunchProcessExited.
func (s *launchSession) Stdin() (io.WriteCloser, error) {
	if s == nil || s.process == nil {
		return nil, ErrLaunchNotStarted
	}
	if err := s.process.inputError(); err != nil {
		return nil, err
	}
	return &s.process.input, nil
}

// SendInput writes one console command to the running process. A trailing
// newline is supplied when the caller did not include one.
func (s *launchSession) SendInput(input string) error {
	stdin, err := s.Stdin()
	if err != nil {
		return err
	}
	if !strings.HasSuffix(input, "\n") {
		input += "\n"
	}
	_, err = io.WriteString(stdin, input)
	return err
}

// Stop cancels a not-yet-started launch or kills a running process. It is safe
// to call concurrently with process startup, SendInput, and process exit.
func (s *launchSession) Stop() error {
	if s == nil || s.process == nil {
		return ErrLaunchNotStarted
	}
	return s.process.stop()
}

// Done is closed once process startup fails, a pending launch is cancelled, or
// the launched process exits.
func (s *launchSession) Done() <-chan struct{} {
	if s == nil || s.process == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return s.process.done
}

func (p *launchProcessSession) inputError() error {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	switch p.state {
	case launchProcessRunning:
		return nil
	case launchProcessExited:
		return ErrLaunchProcessExited
	default:
		return ErrLaunchNotStarted
	}
}

func (p *launchProcessSession) run(cmd *exec.Cmd, onStarted func(pid int) error) error {
	if cmd == nil {
		p.finish()
		return errors.New("launch command is nil")
	}

	// prepareLaunch attaches os.Stdin for non-TUI launches. A launchSession
	// always uses a pipe so the TUI can submit server console commands.
	cmd.Stdin = nil
	stdin, err := cmd.StdinPipe()
	if err != nil {
		p.finish()
		return fmt.Errorf("create process stdin: %w", err)
	}
	p.inputMu.Lock()
	p.stdin = stdin
	p.inputMu.Unlock()

	p.stateMu.Lock()
	if p.stopRequested {
		p.state = launchProcessExited
		p.stateMu.Unlock()
		p.closeInput()
		p.signalDone()
		return ErrLaunchStopped
	}
	p.state = launchProcessStarting
	p.stateMu.Unlock()

	if err := cmd.Start(); err != nil {
		p.finish()
		return err
	}

	p.stateMu.Lock()
	p.process = cmd.Process
	p.state = launchProcessRunning
	if p.stopRequested {
		// Holding stateMu closes the gap between publishing the process and a
		// concurrent Stop. Process.Kill is non-blocking on supported platforms.
		if err := p.process.Kill(); err == nil {
			p.stopIssued = true
		}
	}
	p.stateMu.Unlock()

	var startedErr error
	if onStarted != nil {
		startedErr = onStarted(cmd.Process.Pid)
		if startedErr != nil {
			p.stateMu.Lock()
			if p.process != nil {
				_ = p.process.Kill()
			}
			p.stateMu.Unlock()
		}
	}

	waitErr := cmd.Wait()
	p.stateMu.Lock()
	stopped := p.stopIssued
	p.state = launchProcessExited
	p.process = nil
	p.stateMu.Unlock()
	p.closeInput()
	p.signalDone()
	if startedErr != nil {
		return fmt.Errorf("record launched process: %w", startedErr)
	}

	if stopped {
		if waitErr != nil {
			return fmt.Errorf("%w: %v", ErrLaunchStopped, waitErr)
		}
		return ErrLaunchStopped
	}
	return waitErr
}

func (p *launchProcessSession) stop() error {
	p.stateMu.Lock()
	switch p.state {
	case launchProcessPending, launchProcessStarting:
		// run checks this both before Start and immediately after publishing
		// cmd.Process, so a Stop racing with Start cannot be lost.
		p.stopRequested = true
		p.stateMu.Unlock()
		return nil
	case launchProcessRunning:
		p.stopRequested = true
		err := p.process.Kill()
		if err == nil {
			p.stopIssued = true
		}
		p.stateMu.Unlock()
		if err != nil {
			if errors.Is(err, os.ErrProcessDone) {
				return ErrLaunchProcessExited
			}
			return fmt.Errorf("stop launch process: %w", err)
		}
		// Killing first ensures a write blocked on a full pipe is released
		// before closeInput waits for the input lock.
		p.closeInput()
		return nil
	case launchProcessExited:
		p.stateMu.Unlock()
		return ErrLaunchProcessExited
	default:
		p.stateMu.Unlock()
		return ErrLaunchNotStarted
	}
}

func (p *launchProcessSession) finish() {
	p.stateMu.Lock()
	p.state = launchProcessExited
	p.process = nil
	p.stateMu.Unlock()
	p.closeInput()
	p.signalDone()
}

func (p *launchProcessSession) signalDone() {
	p.doneOnce.Do(func() { close(p.done) })
}

func (p *launchProcessSession) closeInput() error {
	p.inputMu.Lock()
	defer p.inputMu.Unlock()
	if p.stdin == nil {
		return nil
	}
	err := p.stdin.Close()
	p.stdin = nil
	return err
}

func (i *launchSessionInput) Write(data []byte) (int, error) {
	p := i.process
	if p == nil {
		return 0, ErrLaunchNotStarted
	}
	p.inputMu.Lock()
	defer p.inputMu.Unlock()
	if err := p.inputError(); err != nil {
		return 0, err
	}
	if p.stdin == nil {
		return 0, ErrLaunchProcessExited
	}
	return p.stdin.Write(data)
}

func (i *launchSessionInput) Close() error {
	if i.process == nil {
		return ErrLaunchNotStarted
	}
	return i.process.closeInput()
}
