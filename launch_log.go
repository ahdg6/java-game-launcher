package main

import (
	"bytes"
	"errors"
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

type launchOutputMsg struct {
	session *launchSession
	text    string
}
type launchStreamClosedMsg struct{ session *launchSession }
type launchFinishedMsg struct {
	err      error
	output   string
	logPath  string
	duration time.Duration
}

type launchSession struct {
	spec      LaunchSpec
	events    chan struct{}
	writer    *launchLogWriter
	started   time.Time
	process   *launchProcessSession
	onStarted func(pid int) error
}

type launchLogWriter struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	file      *os.File
	events    chan struct{}
	truncated bool
	closed    bool
}

var errLaunchLogClosed = errors.New("启动日志已经关闭")

type LaunchLogInfo struct {
	Path    string
	Name    string
	Size    int64
	ModTime time.Time
}

func newLaunchSession(spec LaunchSpec, cfgPath string) *launchSession {
	// One pending signal is enough: the receiver reads a fresh continuous tail
	// from buffer, so noisy games are coalesced without dropping bytes or
	// blocking their stdout/stderr writers on terminal redraws.
	events := make(chan struct{}, 1)
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

func persistLaunchPreparationFailure(cfgPath, instanceID string, failure error) (string, string) {
	if !instanceIDPattern.MatchString(instanceID) {
		instanceID = defaultInstanceID
	}
	now := time.Now()
	output := fmt.Sprintf(
		"[启动器] 时间: %s\n[启动器] 实例: %s\n\n[启动器] 启动前检查失败: %v\n",
		now.Format(time.RFC3339), instanceID, failure,
	)
	directory := filepath.Join(configDir(cfgPath), "logs", instanceID)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", output + fmt.Sprintf("[启动器] 持久日志不可写: %v\n", err)
	}
	file := createUniqueLaunchLog(directory, now)
	if file == nil {
		return "", output + "[启动器] 持久日志不可写: 无法创建唯一日志文件\n"
	}
	path := file.Name()
	if _, err := io.WriteString(file, output); err != nil {
		output += fmt.Sprintf("[启动器] 持久日志写入失败: %v\n", err)
	}
	_ = file.Close()
	return path, output
}

func latestLaunchLog(cfgPath, instanceID string) (string, string, error) {
	logs, err := listLaunchLogs(cfgPath, instanceID)
	if err != nil || len(logs) == 0 {
		return "", "", err
	}
	text, err := readLaunchLogTail(logs[0].Path)
	return logs[0].Path, text, err
}

func listLaunchLogs(cfgPath, instanceID string) ([]LaunchLogInfo, error) {
	if !instanceIDPattern.MatchString(instanceID) {
		instanceID = defaultInstanceID
	}
	directory := filepath.Join(configDir(cfgPath), "logs", instanceID)
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return []LaunchLogInfo{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取实例日志目录: %w", err)
	}
	logs := make([]LaunchLogInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.EqualFold(filepath.Ext(entry.Name()), ".log") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		logs = append(logs, LaunchLogInfo{
			Path: filepath.Join(directory, entry.Name()), Name: entry.Name(), Size: info.Size(), ModTime: info.ModTime(),
		})
	}
	sort.Slice(logs, func(i, j int) bool {
		if logs[i].ModTime.Equal(logs[j].ModTime) {
			return logs[i].Name > logs[j].Name
		}
		return logs[i].ModTime.After(logs[j].ModTime)
	})
	return logs, nil
}

func readLaunchLogTail(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("打开启动日志: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("读取启动日志: %w", err)
	}
	offset := max(int64(0), info.Size()-maxCapturedLogBytes)
	data := make([]byte, info.Size()-offset)
	read, readErr := file.ReadAt(data, offset)
	if readErr != nil && readErr != io.EOF {
		return "", fmt.Errorf("读取启动日志: %w", readErr)
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
	return text, nil
}

func deleteLaunchLog(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("检查启动日志: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !strings.EqualFold(filepath.Ext(path), ".log") {
		return errors.New("拒绝删除非普通日志文件")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("删除启动日志: %w", err)
	}
	return nil
}

func (w *launchLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, errLaunchLogClosed
	}
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
	select {
	case w.events <- struct{}{}:
	default:
		// A notification is already pending. Its receiver will read the latest
		// continuous tail, including this write.
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
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	if w.file != nil {
		_ = w.file.Close()
	}
	if w.events != nil {
		close(w.events)
	}
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
		if errors.Is(err, ErrLaunchStopped) {
			_, _ = io.WriteString(session.writer, "\n[启动器] 服务器已由用户停止。\n")
		} else if err != nil {
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
		_, ok := <-session.events
		if !ok {
			return launchStreamClosedMsg{session: session}
		}
		return launchOutputMsg{session: session, text: session.writer.output()}
	}
}
