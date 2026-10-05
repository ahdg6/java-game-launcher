package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ahdg6/java-game-launcher/internal/java"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type editMode int

const (
	editNone editMode = iota
	editJavaPath
	editJarPath
	editWorkingDir
	editDataDir
	editJVMArgs
	editGameArgs
	editInstanceName
)

const menuItemCount = 15

type environmentMsg struct {
	instanceID string
	generation uint64
	env        Environment
}

type page uint8

const (
	pageMain page = iota
	pageLog
	pageHistory
	pageZulu
	pageTools
	pageBackups
	pageMods
	pagePreflight
	pageInstances
)

// Editors and the file picker are overlays; all full pages are mutually exclusive.
func (m *model) closePage(current, parent page) {
	if m.page == current {
		m.page = parent
	}
}

type model struct {
	autoLaunchState
	toolsState
	instancesState
	preflightState
	modsState
	backupState
	runtimeState
	historyState
	launchState
	logState
	editorState
	page                page
	launcher            LauncherConfig
	cfg                 Config
	cfgPath             string
	env                 Environment
	discoveryGeneration uint64
	cursor              int
	width               int
	height              int
	loading             bool
	dirty               bool
	confirmQuit         bool
	status              string
	statusErr           bool
	memory              java.MemoryInfo
}

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	labelStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("111"))
)

func newModel(launcher LauncherConfig, cfgPath, initialStatus string, statusErr bool, launchExtraArgs ...string) model {
	active, err := launcher.Active()
	if err != nil {
		launcher = defaultLauncherConfig()
		active, _ = launcher.Active()
		if initialStatus == "" {
			initialStatus = err.Error()
			statusErr = true
		}
	}
	cfg := active.Config()
	input := textinput.New()
	input.Prompt = "> "
	input.CharLimit = 4096
	input.Width = 70
	serverInput := textinput.New()
	serverInput.Prompt = "server> "
	serverInput.CharLimit = 4096
	serverInput.Width = 70
	area := textarea.New()
	area.Placeholder = "每行一个参数"
	area.ShowLineNumbers = true
	area.SetWidth(76)
	area.SetHeight(12)
	logView := viewport.New(80, 18)
	memory := java.DetectMemory()
	result := model{
		launcher: launcher, cfg: cfg, cfgPath: cfgPath, loading: true,
		discoveryGeneration: 1, instancesState: instancesState{instancesCursor: launcherInstanceIndex(launcher, cfg.InstanceID)},
		status: initialStatus, statusErr: statusErr,
		editorState: editorState{input: input, area: area}, logState: logState{serverInput: serverInput, logView: logView}, memory: memory,
		launchState:     launchState{launchExtraArgs: slices.Clone(launchExtraArgs)},
		autoLaunchState: autoLaunchState{autoLaunchCancelled: statusErr},
	}
	result.loadLatestInstanceLog()
	return result
}

func (m model) Init() tea.Cmd {
	return discoverCmd(m.cfg, m.cfgPath, m.discoveryGeneration)
}

func discoverCmd(cfg Config, cfgPath string, generation uint64) tea.Cmd {
	return func() tea.Msg {
		return environmentMsg{
			instanceID: cfg.InstanceID,
			generation: generation,
			env:        discoverEnvironment(cfg, cfgPath),
		}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Input before discovery finishes also suppresses this invocation's countdown.
	switch msg.(type) {
	case tea.KeyMsg, tea.MouseMsg:
		m.cancelAutoLaunch()
	}
	switch msg := msg.(type) {
	case autoLaunchTickMsg:
		return m.updateAutoLaunch(msg)
	case zuluMetadataMsg, zuluInstallMsg:
		return m.updateZulu(msg)
	case toolResultMsg:
		return m.updateTools(msg)
	case backupRestoreMsg:
		return m.updateBackups(msg)
	case preflightResultMsg:
		return m.updatePreflight(msg)
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = max(20, min(100, msg.Width-8))
		m.serverInput.Width = max(20, msg.Width-10)
		m.area.SetWidth(max(30, min(100, msg.Width-6)))
		m.area.SetHeight(max(6, min(16, msg.Height-10)))
		m.logView.Width = max(20, msg.Width-2)
		m.logView.Height = max(5, msg.Height-7)
		if m.picking {
			m.picker.SetHeight(max(5, msg.Height-8))
		}
		return m, nil
	case environmentMsg:
		if msg.instanceID != m.cfg.InstanceID || msg.generation != m.discoveryGeneration {
			return m, nil
		}
		m.env = msg.env
		m.loading = false
		changed := applyAutoSelections(&m.cfg, m.cfgPath, m.env)
		m.dirty = m.dirty || changed
		javaOK := selectedJava(m.cfg, m.cfgPath, m.env)
		jarOK := selectedJar(m.cfg, m.cfgPath, m.env)
		switch {
		case !jarOK:
			m.setStatus("没有找到可执行的游戏 JAR，请使用文件选择器指定", true)
		case !javaOK:
			if java, ok := findSelectedJava(m.cfg, m.cfgPath, m.env); ok && java.Err != nil {
				m.setStatus(java.Err.Error(), true)
			} else {
				required := selectedRequiredJava(m.cfg, m.cfgPath, m.env)
				m.setStatus(fmt.Sprintf("没有找到兼容的 Java（需要 Java %d 或更高）", required), true)
			}
		case changed:
			m.setStatus("自动检测完成，已选用兼容的 Java 和游戏 JAR", false)
		default:
			m.setStatus("检测完成", false)
		}
		if shared := m.sharedDataDirectoryNames(m.cfg.InstanceID); len(shared) > 0 && !m.statusErr {
			m.setStatus("检测完成；警告：当前实例与 "+strings.Join(shared, "、")+" 共用数据目录", true)
		}
		command := m.armAutoLaunch()
		return m, command
	case launchOutputMsg:
		if !m.launching || msg.session != m.activeSession {
			return m, nil
		}
		m.replaceLiveLog(msg.text)
		return m, waitLaunchOutput(m.activeSession)
	case launchStreamClosedMsg:
		if msg.session != m.activeSession {
			return m, nil
		}
		return m, nil
	case launchPreparedMsg:
		return m.acceptLaunchPreparation(msg)
	case launchFinishedMsg:
		return m.finishLaunch(msg)
	}
	if _, ok := msg.(tea.KeyMsg); ok && m.preparing != nil {
		return m, nil
	}

	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "ctrl+c" && m.launching {
		m.setStatus("游戏或服务器仍在运行；请先在游戏中退出，服务器可用 Ctrl+X 安全停止", true)
		return m, nil
	}

	if m.mode != editNone {
		return m.updateEditor(msg)
	}
	if m.picking {
		return m.updatePicker(msg)
	}
	switch m.page {
	case pageLog:
		return m.updateLogView(msg)
	case pageHistory:
		return m.updateHistory(msg)
	case pageZulu:
		return m.updateZulu(msg)
	case pagePreflight:
		return m.updatePreflight(msg)
	case pageBackups:
		return m.updateBackups(msg)
	case pageMods:
		return m.updateMods(msg)
	case pageTools:
		return m.updateTools(msg)
	case pageInstances:
		return m.updateInstances(msg)
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if m.confirmQuit && key.String() != "q" &&
		!((key.String() == "enter" || key.String() == " ") && m.cursor == menuItemCount-1) {
		m.confirmQuit = false
	}
	switch key.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "q":
		if m.launching {
			m.setStatus("游戏或服务器仍在运行，不能退出启动器", true)
			return m, nil
		}
		if m.dirty && !m.confirmQuit {
			m.confirmQuit = true
			m.setStatus("配置尚未保存；再次按 Q 放弃修改并退出，或按 S 保存", true)
			return m, nil
		}
		return m, tea.Quit
	case "up", "k":
		m.cursor = (m.cursor - 1 + menuItemCount) % menuItemCount
	case "down", "j":
		m.cursor = (m.cursor + 1) % menuItemCount
	case "left", "h":
		if m.cursor == 1 {
			return m.switchInstanceBy(-1)
		} else if m.cursor == 2 {
			m.cycleJava(-1)
		} else if m.cursor == 3 {
			m.cycleJar(-1)
		} else if m.cursor == 4 {
			return m.cycleProfile(-1)
		} else if m.cursor == 8 {
			m.cycleJVMPreset(-1)
		}
	case "right", "l":
		if m.cursor == 1 {
			return m.switchInstanceBy(1)
		} else if m.cursor == 2 {
			m.cycleJava(1)
		} else if m.cursor == 3 {
			m.cycleJar(1)
		} else if m.cursor == 4 {
			return m.cycleProfile(1)
		} else if m.cursor == 8 {
			m.cycleJVMPreset(1)
		}
	case "r":
		return m.startRefresh()
	case "s":
		m.save()
	case "d":
		if m.cursor == 8 {
			preset := java.ResolveJVMPreset(java.PresetAuto, m.memory)
			m.cfg.JVMPreset = preset.ID
			m.cfg.JVMArgs = preset.Args
			m.dirty = true
			m.setStatus("已恢复自动 JVM 预设："+preset.Description, false)
		}
	case "enter", " ":
		return m.activate()
	}
	return m, nil
}

func (m model) activate() (tea.Model, tea.Cmd) {
	if m.launching && m.cursor == 0 {
		m.setStatus("游戏进程仍在运行；可打开日志页面查看状态", true)
		return m, nil
	}
	switch m.cursor {
	case 0:
		return m.startConfiguredGame()
	case 1:
		m.page = pageInstances
		m.instancesCursor = launcherInstanceIndex(m.launcher, m.cfg.InstanceID)
		m.confirmDeleteInstance = false
	case 2:
		m.page = pageZulu
		m.zuluStatus = "按 R 查询 Azul 官方最新 LTS，按 P 选择本地 Java"
		m.zuluStatusErr = false
		m.confirmZuluInstall = false
	case 3:
		return m, m.beginPathPicker(editJarPath, "选择可执行游戏 JAR")
	case 4:
		return m.cycleProfile(1)
	case 5:
		return m, m.beginPathPicker(editWorkingDir, "选择工作目录")
	case 6:
		if effectiveAdapterForModel(m).DataDirectoryProperty() == "" {
			m.setStatus("当前通用配置没有专用数据目录参数；可在 JVM 参数中添加游戏要求的 -D 属性", true)
			return m, nil
		}
		return m, m.beginPathPicker(editDataDir, "选择 "+effectiveAdapterForModel(m).DisplayName()+" 数据目录")
	case 7:
		if effectiveAdapterForModel(m).ID() != profileMindustry {
			m.setStatus("Mindustry 工具仅在识别或选择 Mindustry 配置后可用", true)
			return m, nil
		}
		m.page = pageTools
		m.refreshBackupCount()
	case 8:
		m.beginArgsEditor(editJVMArgs, "编辑 JVM 参数", m.cfg.JVMArgs)
	case 9:
		m.beginArgsEditor(editGameArgs, "编辑游戏参数", m.cfg.GameArgs)
	case 10:
		return m.startPreflight()
	case 11:
		m.page = pageHistory
		m.refreshHistory()
	case 12:
		return m.startRefresh()
	case 13:
		m.save()
	case 14:
		if m.launching {
			m.setStatus("游戏或服务器仍在运行，不能退出启动器", true)
			return m, nil
		}
		if m.dirty && !m.confirmQuit {
			m.confirmQuit = true
			m.setStatus("配置尚未保存；再次执行退出可放弃修改，或按 S 保存", true)
			return m, nil
		}
		return m, tea.Quit
	}
	return m, nil
}

func (m *model) startRefresh() (tea.Model, tea.Cmd) {
	if m.loading {
		m.setStatus("正在检测，请稍候", false)
		return *m, nil
	}
	m.loading = true
	m.discoveryGeneration++
	m.setStatus("正在扫描本地 JDK、JAVA_HOME 和 PATH…", false)
	return *m, discoverCmd(m.cfg, m.cfgPath, m.discoveryGeneration)
}

func (m *model) save() {
	m.syncActiveInstance()
	if err := saveLauncherConfig(m.cfgPath, m.launcher); err != nil {
		m.setStatus(err.Error(), true)
		return
	}
	m.dirty = false
	m.confirmQuit = false
	m.setStatus("配置已保存到 "+m.cfgPath, false)
}

func (m *model) setStatus(status string, isError bool) {
	m.status, m.statusErr = status, isError
}
