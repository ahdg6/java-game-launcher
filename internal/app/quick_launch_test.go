package app

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func successfulStartupModel(t *testing.T) (model, environmentMsg) {
	t.Helper()
	launcher := defaultLauncherConfig()
	launcher.Instances[0].JavaPath = "java"
	launcher.Instances[0].JarPath = "game.jar"
	m := newModel(launcher, filepath.Join(t.TempDir(), configFileName), "", false)
	if err := recordSuccessfulLaunch(m.cfgPath, m.configForNextLaunch()); err != nil {
		t.Fatal(err)
	}
	discovered := environmentMsg{instanceID: m.cfg.InstanceID, generation: m.discoveryGeneration, env: Environment{
		Java: []JavaCandidate{{Path: resolveConfigPath(m.cfgPath, m.cfg.JavaPath), Version: 21}},
		Jars: []JarInfo{{Path: resolveConfigPath(m.cfgPath, m.cfg.JarPath), RequiredJavaVersion: 17}},
	}}
	return m, discovered
}

func TestQuickLaunchCountdownStartsAfterDiscoveryAndRunsOnlyOnce(t *testing.T) {
	m, discovered := successfulStartupModel(t)
	if m.autoLaunchRemaining != 0 {
		t.Fatal("countdown started before discovery")
	}
	updated, command := m.Update(discovered)
	m = updated.(model)
	if command == nil || m.autoLaunchRemaining != 3 || !strings.Contains(m.View(), "3 秒后自动启动") {
		t.Fatalf("countdown not visible: remaining=%d command=%v", m.autoLaunchRemaining, command != nil)
	}
	token := m.autoLaunchToken
	for _, remaining := range []int{2, 1, 0} {
		updated, command = m.Update(autoLaunchTickMsg{token: token})
		m = updated.(model)
		if m.autoLaunchRemaining != remaining || command == nil {
			t.Fatalf("tick: remaining=%d want=%d", m.autoLaunchRemaining, remaining)
		}
	}
	if m.preparing == nil || !m.autoLaunchCancelled {
		t.Fatal("countdown did not consume itself before preparing")
	}
	result := command().(launchPreparedMsg) // Missing fixture JAR makes this attempt fail.
	if result.err == nil {
		t.Fatal("expected missing fixture JAR failure")
	}
	updated, _ = m.Update(result)
	m = updated.(model)
	if hasSuccessfulLaunch(m.cfgPath, m.configForNextLaunch()) {
		t.Fatal("failed attempt left successful eligibility")
	}
	updated, command = m.Update(autoLaunchTickMsg{token: token})
	m = updated.(model)
	if command != nil || m.preparing != nil {
		t.Fatal("stale timer retried failed launch")
	}
	updated, command = m.Update(discovered)
	if command != nil || updated.(model).autoLaunchRemaining != 0 {
		t.Fatal("rediscovery rearmed countdown")
	}
}

func TestQuickLaunchAnyInputCancelsBeforeOrDuringDiscovery(t *testing.T) {
	for _, beforeDiscovery := range []bool{true, false} {
		for _, input := range []tea.Msg{keyRune('x'), tea.KeyMsg{Type: tea.KeyDown}, tea.MouseMsg{}} {
			m, discovered := successfulStartupModel(t)
			if !beforeDiscovery {
				updated, _ := m.Update(discovered)
				m = updated.(model)
			}
			token := m.autoLaunchToken
			updated, _ := m.Update(input)
			m = updated.(model)
			updated, command := m.Update(discovered)
			m = updated.(model)
			if command != nil || m.autoLaunchRemaining != 0 || !m.autoLaunchCancelled {
				t.Fatalf("input %T did not cancel (early=%v)", input, beforeDiscovery)
			}
			updated, command = m.Update(autoLaunchTickMsg{token: token})
			m = updated.(model)
			if command != nil || m.preparing != nil {
				t.Fatal("cancelled timer started launch")
			}
			if !hasSuccessfulLaunch(m.cfgPath, m.configForNextLaunch()) {
				t.Fatal("cancelling countdown consumed persistent eligibility")
			}
		}
	}
}

func TestQuickLaunchEnterStartsImmediatelyAndLaunchIsFirst(t *testing.T) {
	m, discovered := successfulStartupModel(t)
	updated, _ := m.Update(discovered)
	m = updated.(model)
	view := m.View()
	if m.cursor != 0 || strings.Index(view, "› 启动游戏") < 0 || strings.Index(view, "› 启动游戏") > strings.Index(view, "实例  ") {
		t.Fatalf("launch is not first selected item: %s", view)
	}
	token := m.autoLaunchToken
	updated, command := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if command == nil || m.preparing == nil || m.autoLaunchRemaining != 0 {
		t.Fatal("Enter did not launch immediately")
	}
	updated, command = m.Update(autoLaunchTickMsg{token: token})
	if command != nil || updated.(model).preparing != m.preparing {
		t.Fatal("old countdown duplicated manual launch")
	}
}

func TestQuickLaunchNoAutoFlagAndChangedConfigDoNotArm(t *testing.T) {
	options, err := parseCLIOptions([]string{"--no-auto-launch", "--", "game-arg"}, io.Discard)
	if err != nil || !options.noAutoLaunch || len(options.gameArgs) != 1 {
		t.Fatalf("parsed options: %#v %v", options, err)
	}
	for _, change := range []func(*model){
		func(m *model) { m.cancelAutoLaunch() },
		func(m *model) { m.cfg.GameArgs = []string{"changed"} },
		func(m *model) { m.launchExtraArgs = []string{"one-shot"} },
		func(m *model) { m.cfg.JVMArgs = append(m.cfg.JVMArgs, "-Xint") },
		func(m *model) { m.cfg.DataDirectory = "different-data" },
	} {
		m, discovered := successfulStartupModel(t)
		change(&m)
		updated, command := m.Update(discovered)
		if command != nil || updated.(model).autoLaunchRemaining != 0 {
			t.Fatal("disabled or changed config armed countdown")
		}
	}
}

func TestLaunchSuccessHistoryIsScopedAndMalformedRecordsAreIgnored(t *testing.T) {
	m, _ := successfulStartupModel(t)
	if !hasSuccessfulLaunch(m.cfgPath, m.cfg) {
		t.Fatal("successful configuration not recognized")
	}
	other := m.cfg
	other.InstanceID = "other"
	if hasSuccessfulLaunch(m.cfgPath, other) || hasSuccessfulLaunch(filepath.Join(filepath.Dir(m.cfgPath), "other.json"), m.cfg) {
		t.Fatal("success record crossed config or instance boundary")
	}
	if err := os.WriteFile(launchSuccessPath(m.cfgPath, m.cfg.InstanceID), []byte(`{"version":999}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if hasSuccessfulLaunch(m.cfgPath, m.cfg) {
		t.Fatal("unknown record schema was accepted")
	}
}

func TestQuickLaunchHistoryWriteFailureDoesNotBlockManualAttempt(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		m := newModel(defaultLauncherConfig(), filepath.Join(t.TempDir(), configFileName), "", false)
		m.loading = false
		path := launchSuccessPath(m.cfgPath, m.cfg.InstanceID)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "keep"), []byte("occupied"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, command := m.startLaunchPreparationFor("", automatic)
		result := command().(launchPreparedMsg)
		if result.historyWarning == nil || result.recordSuccess {
			t.Fatal("unwritable history did not suppress success recording")
		}
		if automatic && (result.saved || !errors.Is(result.err, result.historyWarning)) {
			t.Fatal("automatic attempt did not fail closed")
		}
		if !automatic && !result.saved {
			t.Fatal("manual attempt stopped before saving and validating game configuration")
		}
	}
}

func TestSuccessRecordingCapturesLaunchedConfigAndExcludesSafeMode(t *testing.T) {
	m := newModel(defaultLauncherConfig(), filepath.Join(t.TempDir(), configFileName), "", false)
	m.cfg.GameArgs = []string{"original"}
	launched := m.configForNextLaunch()
	spec := LaunchSpec{InstanceID: m.cfg.InstanceID, Command: exec.Command(os.Args[0], "-test.run=^$")}
	updated, _ := m.startLaunchSpec(spec)
	m = updated.(model)
	m.cfg.GameArgs[0] = "edited-while-running"
	result := runLaunchSession(m.activeSession)().(launchFinishedMsg)
	if result.err != nil {
		t.Fatal(result.err)
	}
	if !hasSuccessfulLaunch(m.cfgPath, launched) || hasSuccessfulLaunch(m.cfgPath, m.configForNextLaunch()) {
		t.Fatal("success did not retain captured launch configuration")
	}
	updated, _ = m.startLaunchSpecWithSafeMode(LaunchSpec{Command: exec.Command(os.Args[0], "-test.run=^$")}, filepath.Join(t.TempDir(), "data"))
	safe := updated.(model)
	defer safe.activeSession.writer.close()
	if safe.activeSession.onSuccess != nil {
		t.Fatal("safe-mode success could authorize normal launch")
	}
	// Even a failed safe-mode preparation invalidates an older normal success.
	safe.loading, safe.launching = false, false
	_, command := safe.startSafeMode(filepath.Join(t.TempDir(), "data"))
	_ = command()
	if hasSuccessfulLaunch(m.cfgPath, launched) {
		t.Fatal("safe-mode attempt retained previous success")
	}
}

func TestStoppedAndFailedProcessesDoNotRecordSuccessfulLaunch(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		root := t.TempDir()
		session := newLaunchSession(LaunchSpec{Command: exec.Command(filepath.Join(root, "missing-java")), InteractiveConsole: true}, filepath.Join(root, configFileName))
		session.onSuccess = func() error { t.Error("failed or cancelled process recorded success"); return nil }
		if stopped {
			if err := session.Stop(); err != nil {
				t.Fatal(err)
			}
		}
		result := runLaunchSession(session)().(launchFinishedMsg)
		if result.err == nil {
			t.Fatal("expected unsuccessful process")
		}
	}
	session := newServerTestSession(t)
	session.spec.InteractiveConsole = true
	session.onSuccess = func() error { t.Error("forced server termination recorded success"); return nil }
	done := runSessionAsync(session)
	_ = waitForSessionStdin(t, session)
	if err := session.Stop(); err != nil {
		t.Fatal(err)
	}
	if result := waitForFinished(t, done); !errors.Is(result.err, ErrLaunchStopped) {
		t.Fatalf("stop result: %v", result.err)
	}
}

func TestDiagnosticInvocationsLeaveQuickLaunchHistoryUntouched(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("JAVA_HOME", "")
	for _, options := range []cliOptions{{diagnose: true}, {dryRun: true}, {preflight: true}, {launch: true, dryRun: true}} {
		m, _ := successfulStartupModel(t)
		options.configPath = m.cfgPath
		var output bytes.Buffer
		_ = runCLI(m.cfg, &m.launcher, options, strings.NewReader(""), &output, &output)
		if !hasSuccessfulLaunch(m.cfgPath, m.cfg) {
			t.Fatalf("diagnostic options consumed history: %#v", options)
		}
	}
}

func TestSuccessHistoryWriteErrorDoesNotFailCompletedGame(t *testing.T) {
	root := t.TempDir()
	session := newLaunchSession(LaunchSpec{Command: exec.Command(os.Args[0], "-test.run=^$")}, filepath.Join(root, configFileName))
	session.onSuccess = func() error { return errors.New("history is read-only") }
	result := runLaunchSession(session)().(launchFinishedMsg)
	if result.err != nil || result.cleanupErr != nil {
		t.Fatalf("metadata error failed completed game: %#v", result)
	}
	if !strings.Contains(result.output, "history is read-only") {
		t.Fatalf("metadata warning missing from log: %q", result.output)
	}
}
