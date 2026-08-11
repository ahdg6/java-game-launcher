package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const maxCapturedLogBytes = 4 << 20

type launchOutputMsg struct{ text string }
type launchStreamClosedMsg struct{}
type launchFinishedMsg struct {
	err      error
	output   string
	logPath  string
	duration time.Duration
}

type launchSession struct {
	spec    LaunchSpec
	events  chan string
	writer  *launchLogWriter
	started time.Time
}

type launchLogWriter struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	file      *os.File
	events    chan string
	truncated bool
}

func newLaunchSession(spec LaunchSpec, cfgPath string) *launchSession {
	events := make(chan string, 256)
	writer := &launchLogWriter{events: events}
	logDir := filepath.Join(configDir(cfgPath), "logs")
	if err := os.MkdirAll(logDir, 0o755); err == nil {
		name := "java-game-" + time.Now().Format("20060102-150405.000") + ".log"
		writer.file, _ = os.Create(filepath.Join(logDir, name))
	}
	session := &launchSession{spec: spec, events: events, writer: writer, started: time.Now()}
	header := fmt.Sprintf("[启动器] 时间: %s\n[启动器] 工作目录: %s\n[启动器] 命令: %s\n\n",
		session.started.Format(time.RFC3339), spec.WorkingDir, formatCommand(spec))
	_, _ = writer.Write([]byte(header))
	return session
}

func (w *launchLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		_, _ = w.file.Write(p)
	}
	if len(p) >= maxCapturedLogBytes {
		w.buffer.Reset()
		_, _ = w.buffer.Write(p[len(p)-maxCapturedLogBytes:])
		w.truncated = true
	} else {
		overflow := w.buffer.Len() + len(p) - maxCapturedLogBytes
		if overflow > 0 {
			old := append([]byte(nil), w.buffer.Bytes()[overflow:]...)
			w.buffer.Reset()
			_, _ = w.buffer.Write(old)
			w.truncated = true
		}
		_, _ = w.buffer.Write(p)
	}
	chunk := string(append([]byte(nil), p...))
	select {
	case w.events <- chunk:
	default:
		// The complete tail remains in buffer and is delivered when the process
		// exits, so a noisy game cannot block on a slow terminal redraw.
	}
	return len(p), nil
}

func (w *launchLogWriter) output() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	output := w.buffer.String()
	if w.truncated {
		output = "[启动器] TUI 仅保留最后 4 MiB；完整内容请查看日志文件。\n\n" + output
	}
	return output
}

func (w *launchLogWriter) close() {
	w.mu.Lock()
	if w.file != nil {
		_ = w.file.Close()
	}
	close(w.events)
	w.mu.Unlock()
}

func (w *launchLogWriter) logPath() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return ""
	}
	return w.file.Name()
}

func runLaunchSession(session *launchSession) tea.Cmd {
	return func() tea.Msg {
		if err := ensureLaunchDirectories(session.spec); err != nil {
			_, _ = fmt.Fprintf(session.writer, "[启动器] 启动前检查失败: %v\n", err)
			output := session.writer.output()
			path := session.writer.logPath()
			session.writer.close()
			return launchFinishedMsg{err: err, output: output, logPath: path, duration: time.Since(session.started)}
		}
		cmd := session.spec.Command
		cmd.Stdin = nil
		cmd.Stdout = session.writer
		cmd.Stderr = session.writer
		err := cmd.Run()
		if err != nil {
			_, _ = fmt.Fprintf(session.writer, "\n[启动器] 游戏进程异常结束: %v\n", err)
		} else {
			_, _ = io.WriteString(session.writer, "\n[启动器] 游戏进程已退出。\n")
		}
		output := session.writer.output()
		path := session.writer.logPath()
		session.writer.close()
		return launchFinishedMsg{
			err: err, output: output, logPath: path, duration: time.Since(session.started),
		}
	}
}

func waitLaunchOutput(session *launchSession) tea.Cmd {
	return func() tea.Msg {
		text, ok := <-session.events
		if !ok {
			return launchStreamClosedMsg{}
		}
		return launchOutputMsg{text: text}
	}
}
