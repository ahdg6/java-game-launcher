package app

import (
	"errors"
	"fmt"
	"slices"

	"github.com/ahdg6/java-game-launcher/internal/diagnostics"
	"github.com/ahdg6/java-game-launcher/internal/mindustry"
	tea "github.com/charmbracelet/bubbletea"
)

type launchState struct {
	preparing       *launchPreparation
	launching       bool
	activeSession   *launchSession
	launchExtraArgs []string
}

// launchPreparation identifies one immutable request. Keys that could change the
// configuration, switch instances or exit are held until its result arrives.
type launchPreparation struct {
	instanceID string
}

type launchPreparedMsg struct {
	request     *launchPreparation
	spec        LaunchSpec
	saved       bool
	safeDataDir string
	err         error
}

func (m model) startConfiguredGame() (tea.Model, tea.Cmd) {
	return m.startLaunchPreparation("")
}

func (m model) startLaunchPreparation(safeDataDir string) (tea.Model, tea.Cmd) {
	if m.loading || m.launching || m.operationBusy() {
		m.setStatus("正在检测、准备启动或运行，请稍候", true)
		return m, nil
	}
	m.syncActiveInstance()
	request := &launchPreparation{instanceID: m.cfg.InstanceID}
	m.preparing = request
	m.confirmQuit = false
	m.page = pageLog
	m.setStatus("正在校验启动配置…", false)
	cfg, configPath, launcher := m.configForNextLaunch(), m.cfgPath, m.launcher.clone()
	return m, func() tea.Msg {
		result := launchPreparedMsg{request: request, safeDataDir: safeDataDir}
		if _, err := recoverInstance(cfg, configPath); err != nil {
			result.err = fmt.Errorf("启动前无法完成安全模式恢复：%w", err)
			return result
		}
		if err := saveLauncherConfig(configPath, launcher); err != nil {
			result.err = err
			return result
		}
		result.saved = true
		result.spec, result.err = prepareLaunch(cfg, configPath)
		return result
	}
}

func (m model) acceptLaunchPreparation(msg launchPreparedMsg) (tea.Model, tea.Cmd) {
	if m.preparing == nil || msg.request != m.preparing || msg.request.instanceID != m.cfg.InstanceID {
		return m, nil
	}
	m.preparing = nil
	if msg.saved {
		m.dirty = false
	}
	if msg.err != nil {
		return m.showPrepareLaunchFailure(msg.err)
	}
	return m.startLaunchSpecWithSafeMode(msg.spec, msg.safeDataDir)
}

func (m model) configForNextLaunch() Config {
	cfg := m.cfg
	cfg.JVMArgs = slices.Clone(m.cfg.JVMArgs)
	cfg.GameArgs = append(slices.Clone(m.cfg.GameArgs), m.launchExtraArgs...)
	return cfg
}

func (m model) showPrepareLaunchFailure(failure error) (tea.Model, tea.Cmd) {
	path, output := persistLaunchPreparationFailure(m.cfgPath, m.cfg.InstanceID, failure)
	m.activeSession = nil
	m.launching = false
	m.page = pageLog
	m.logPath = path
	m.logText = normalizeLog(output)
	m.launchErr = failure
	m.historyLogFailed = false
	m.launchCleanupErr = nil
	m.diagnostics = diagnostics.AnalyzeLaunchFailure(m.logText, failure)
	m.showAnalysis = len(m.diagnostics) > 0
	m.logView.SetContent(m.logDisplayContent())
	m.logView.GotoTop()
	m.refreshHistory()
	m.setStatus("启动前检查失败："+failure.Error(), true)
	return m, nil
}

func (m model) startLaunchSpec(spec LaunchSpec) (tea.Model, tea.Cmd) {
	return m.startLaunchSpecWithSafeMode(spec, "")
}

func (m model) startLaunchSpecWithSafeMode(spec LaunchSpec, safeDataDir string) (tea.Model, tea.Cmd) {
	m.setStatus("正在启动: "+formatCommand(spec), false)
	session := newLaunchSession(spec, m.cfgPath)
	if safeDataDir != "" {
		stateDir := recoveryStateDirectory(m.cfgPath, m.cfg.InstanceID)
		session.beforeStart = func() error { return mindustry.BeginSafeMode(safeDataDir, stateDir) }
		session.onStarted = func(pid int) error { return mindustry.BindSafeModeProcess(safeDataDir, stateDir, pid) }
		session.afterExit = func() error { return mindustry.EndSafeMode(safeDataDir, stateDir) }
	}
	m.activeSession = session
	m.launching = true
	m.consoleInput = false
	m.serverStopPending = false
	m.page = pageLog
	m.launchErr = nil
	m.historyLogFailed = false
	m.launchCleanupErr = nil
	m.diagnostics = nil
	m.showAnalysis = false
	m.logPath = session.writer.logPath()
	m.logText = ""
	m.logView.SetContent("")
	return m, tea.Batch(runLaunchSession(session), waitLaunchOutput(session))
}

func (m model) finishLaunch(msg launchFinishedMsg) (tea.Model, tea.Cmd) {
	if !m.launching || msg.session != m.activeSession {
		return m, nil
	}
	wasServer := m.activeSession != nil && m.activeSession.spec.InteractiveConsole
	m.launching = false
	m.consoleInput = false
	m.serverStopPending = false
	m.serverInput.Blur()
	safeModeRestoreErr := msg.cleanupErr
	m.logText = normalizeLog(msg.output)
	m.logPath = msg.logPath
	stopped := errors.Is(msg.err, ErrLaunchStopped)
	m.launchErr = msg.err
	m.historyLogFailed = false
	m.launchCleanupErr = safeModeRestoreErr
	if stopped {
		m.launchErr = nil
		m.diagnostics = nil
	} else {
		m.diagnostics = diagnostics.AnalyzeLaunchFailure(m.logText, msg.err)
	}
	m.showAnalysis = len(m.diagnostics) > 0
	m.logView.SetContent(m.logDisplayContent())
	if m.showAnalysis {
		m.logView.GotoTop()
	} else {
		m.logView.GotoBottom()
	}
	if safeModeRestoreErr != nil {
		m.setStatus("游戏已退出，但安全模式恢复失败："+safeModeRestoreErr.Error(), true)
	} else if stopped {
		m.setStatus(fmt.Sprintf("服务器已停止（运行 %.1fs）", msg.duration.Seconds()), false)
	} else if msg.err != nil {
		m.setStatus(fmt.Sprintf("启动失败（%.1fs）：%s", msg.duration.Seconds(), msg.err), true)
	} else if wasServer {
		m.setStatus(fmt.Sprintf("服务器已退出（运行 %.1fs）", msg.duration.Seconds()), false)
	} else {
		m.setStatus(fmt.Sprintf("游戏已退出（运行 %.1fs）", msg.duration.Seconds()), false)
	}
	m.refreshHistory()
	return m, nil
}

// Background operations own their captured instance until completion.
func (m model) operationBusy() bool {
	return m.preparing != nil || m.zuluBusy || m.toolBusy || m.backupBusy || m.preflightBusy
}
