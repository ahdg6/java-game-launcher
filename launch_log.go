package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	spec      LaunchSpec
	events    chan string
	writer    *launchLogWriter
	started   time.Time
	process   *launchProcessSession
	onStarted func(pid int) error
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
	instanceID := spec.InstanceID
	if !instanceIDPattern.MatchString(instanceID) {
		instanceID = defaultInstanceID
	}
	logDir := filepath.Join(configDir(cfgPath), "logs", instanceID)
	if err := os.MkdirAll(logDir, 0o755); err == nil {
		writer.file = createUniqueLaunchLog(logDir, time.Now())
	}
	session := &launchSession{
		spec: spec, events: events, writer: writer, started: time.Now(),
		process: newLaunchProcessSession(),
	}
	header := fmt.Sprintf("[启动器] 时间: %s\n[启动器] 工作目录: %s\n[启动器] 命令: %s\n\n",
		session.started.Format(time.RFC3339), spec.WorkingDir, formatCommand(spec))
	_, _ = writer.Write([]byte(header))
	return session
}

func createUniqueLaunchLog(directory string, now time.Time) *os.File {
	base := "java-game-" + now.Format("20060102-150405.000")
	for number := 0; number < 1000; number++ {
		name := base + ".log"
		if number > 0 {
			name = fmt.Sprintf("%s-%d.log", base, number)
		}
		file, err := os.OpenFile(filepath.Join(directory, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return file
		}
		if !os.IsExist(err) {
			return nil
		}
	}
	return nil
}

func latestLaunchLog(cfgPath, instanceID string) (string, string, error) {
	if !instanceIDPattern.MatchString(instanceID) {
		instanceID = defaultInstanceID
	}
	directory := filepath.Join(configDir(cfgPath), "logs", instanceID)
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("读取实例日志目录: %w", err)
	}
	type candidate struct {
		path    string
		name    string
		modTime time.Time
	}
	candidates := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.EqualFold(filepath.Ext(entry.Name()), ".log") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		candidates = append(candidates, candidate{
			path: filepath.Join(directory, entry.Name()), name: entry.Name(), modTime: info.ModTime(),
		})
	}
	if len(candidates) == 0 {
		return "", "", nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].modTime.Equal(candidates[j].modTime) {
			return candidates[i].name > candidates[j].name
		}
		return candidates[i].modTime.After(candidates[j].modTime)
	})
	selected := candidates[0]
	file, err := os.Open(selected.path)
	if err != nil {
		return "", "", fmt.Errorf("打开上次启动日志: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", "", fmt.Errorf("读取上次启动日志: %w", err)
	}
	offset := max(int64(0), info.Size()-maxCapturedLogBytes)
	data := make([]byte, info.Size()-offset)
	read, readErr := file.ReadAt(data, offset)
	if readErr != nil && readErr != io.EOF {
		return "", "", fmt.Errorf("读取上次启动日志: %w", readErr)
	}
	data = data[:read]
	if offset > 0 {
		if newline := bytes.IndexByte(data, '\n'); newline >= 0 {
			data = data[newline+1:]
		}
	}
	text := string(data)
	if offset > 0 {
		text = "[启动器] 仅载入上次日志最后 4 MiB；完整内容请查看日志文件。\n\n" + text
	}
	return selected.path, text, nil
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
			session.process.finish()
			_, _ = fmt.Fprintf(session.writer, "[启动器] 启动前检查失败: %v\n", err)
			output := session.writer.output()
			path := session.writer.logPath()
			session.writer.close()
			return launchFinishedMsg{err: err, output: output, logPath: path, duration: time.Since(session.started)}
		}
		cmd := session.spec.Command
		cmd.Stdout = session.writer
		cmd.Stderr = session.writer
		err := session.process.run(cmd, session.onStarted)
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
