package main

import (
	"errors"
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

func TestAppendLogKeepsScrolledPositionAndFollowsBottom(t *testing.T) {
	m := newModel(defaultLauncherConfig(), filepath.Join(t.TempDir(), configFileName), "", false)
	m.loading = false
	m.logView.Height = 4
	m.logText = strings.Repeat("line\n", 20)
	m.logView.SetContent(m.logText)
	m.logView.GotoBottom()
	m.logView.ScrollUp(3)
	wantedOffset := m.logView.YOffset
	m.appendLog("new while reading\n")
	if m.logView.YOffset != wantedOffset {
		t.Fatalf("scrolled offset changed from %d to %d", wantedOffset, m.logView.YOffset)
	}
	m.logView.GotoBottom()
	m.appendLog("followed\n")
	if !m.logView.AtBottom() {
		t.Fatal("viewport stopped following while already at bottom")
	}
}

func TestNoModRetryPreparationFailureRemainsInLogView(t *testing.T) {
	root := t.TempDir()
	m := newModel(defaultLauncherConfig(), filepath.Join(root, configFileName), "", false)
	m.loading = false
	m.cfg.GameProfile = profileMindustry
	m.cfg.DataDirectory = "game_data"
	m.cfg.JavaPath = "missing-java"
	m.cfg.JarPath = "missing.jar"
	m.launchErr = errors.New("previous launch failed")
	m.showLog = true
	m.activeSession = &launchSession{spec: LaunchSpec{
		Jar:           JarInfo{MainClass: "mindustry.desktop.DesktopLauncher"},
		NeedsGraphics: true,
	}}
	if !m.canRetryWithoutMindustryMods() {
		t.Fatal("retry should be available")
	}
	updated, command := m.Update(keyRune('m'))
	m = updated.(model)
	if command != nil || !m.showLog || m.launchErr == nil || m.logPath == "" || !strings.Contains(m.logText, "启动前检查失败") {
		t.Fatalf("retry failure state: log=%v err=%v path=%q text=%q", m.showLog, m.launchErr, m.logPath, m.logText)
	}
}

func TestLaunchWriterCoalescesSignalsButKeepsContinuousTail(t *testing.T) {
	events := make(chan struct{}, 1)
	writer := &launchLogWriter{events: events}
	for _, chunk := range []string{"\x1b[3", "1mred\x1b[0m ", "你"} {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(events); got != 1 {
		t.Fatalf("pending event count = %d, want 1", got)
	}
	if got := normalizeLog(writer.output()); got != "red 你" {
		t.Fatalf("coalesced continuous output = %q", got)
	}
}

func TestLaunchWriterCloseIsIdempotentAndRejectsLaterWrites(t *testing.T) {
	events := make(chan struct{}, 1)
	writer := &launchLogWriter{events: events}
	if _, err := writer.Write([]byte("before")); err != nil {
		t.Fatal(err)
	}
	writer.close()
	writer.close()
	if written, err := writer.Write([]byte("after")); written != 0 || !errors.Is(err, errLaunchLogClosed) {
		t.Fatalf("write after close = %d, %v", written, err)
	}
	if got := writer.output(); got != "before" {
		t.Fatalf("closed output = %q", got)
	}
}

func TestTUIIgnoresOutputFromPreviousLaunchSession(t *testing.T) {
	m := newModel(defaultLauncherConfig(), filepath.Join(t.TempDir(), configFileName), "", false)
	active := &launchSession{events: make(chan struct{}, 1), writer: &launchLogWriter{events: make(chan struct{}, 1)}}
	stale := &launchSession{}
	m.activeSession = active
	m.launching = true
	updated, command := m.Update(launchOutputMsg{session: stale, text: "stale"})
	m = updated.(model)
	if command != nil || strings.Contains(m.logText, "stale") {
		t.Fatalf("stale output was accepted: text=%q command=%v", m.logText, command != nil)
	}
}

func TestFailedMindustryDesktopOffersExplicitNoModRetry(t *testing.T) {
	m := newModel(defaultLauncherConfig(), filepath.Join(t.TempDir(), configFileName), "", false)
	m.loading = false
	m.cfg.DataDirectory = "game_data"
	m.launchErr = errors.New("exit status 1")
	m.activeSession = &launchSession{spec: LaunchSpec{
		Jar:           JarInfo{MainClass: "mindustry.desktop.DesktopLauncher"},
		NeedsGraphics: true,
	}}
	if !m.canRetryWithoutMindustryMods() || !strings.Contains(m.logViewPage(), "M 仅本次无模组重试") {
		t.Fatalf("desktop failure did not offer retry: %s", m.logViewPage())
	}
	m.activeSession.spec.InteractiveConsole = true
	if m.canRetryWithoutMindustryMods() {
		t.Fatal("server failure offered no-mod desktop retry")
	}
	m.activeSession.spec.InteractiveConsole = false
	m.activeSession.spec.Jar.MainClass = "example.Game"
	if m.canRetryWithoutMindustryMods() {
		t.Fatal("generic game failure offered Mindustry retry")
	}
	m.activeSession.spec.Jar.MainClass = "mindustry.desktop.DesktopLauncher"
	m.cfg.DataDirectory = ""
	if m.canRetryWithoutMindustryMods() {
		t.Fatal("unmanaged data directory offered no-mod retry")
	}
}

func TestPreparationFailureIsPersistedAndShownInLogView(t *testing.T) {
	configDirectory := t.TempDir()
	m := newModel(defaultLauncherConfig(), filepath.Join(configDirectory, configFileName), "", false)
	m.loading = false
	m.cfg.JavaPath = ""
	updated, command := m.startConfiguredGame()
	m = updated.(model)
	if command != nil || !m.showLog || m.launchErr == nil || len(m.diagnostics) == 0 {
		t.Fatalf("preparation failure state: showLog=%v err=%v diagnostics=%#v", m.showLog, m.launchErr, m.diagnostics)
	}
	if m.logPath == "" || !strings.Contains(m.logText, "启动前检查失败") {
		t.Fatalf("preparation failure log path=%q text=%q", m.logPath, m.logText)
	}
	if _, err := os.Stat(m.logPath); err != nil {
		t.Fatalf("persistent preparation log: %v", err)
	}
	logs, err := listLaunchLogs(m.cfgPath, defaultInstanceID)
	if err != nil || len(logs) != 1 || logs[0].Path != m.logPath {
		t.Fatalf("history logs=%#v err=%v", logs, err)
	}
}

func TestSafeModePreparationFailureUsesPersistentLogView(t *testing.T) {
	configDirectory := t.TempDir()
	m := newModel(defaultLauncherConfig(), filepath.Join(configDirectory, configFileName), "", false)
	m.loading = false
	m.showTools = true
	m.cfg.JavaPath = ""
	updated, command := m.startMindustrySafeMode(filepath.Join(configDirectory, "game_data"))
	m = updated.(model)
	if command != nil || m.showTools || !m.showLog || m.logPath == "" || m.launchErr == nil {
		t.Fatalf("safe mode preparation failure: tools=%v log=%v path=%q err=%v", m.showTools, m.showLog, m.logPath, m.launchErr)
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
