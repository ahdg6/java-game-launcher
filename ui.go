package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/filepicker"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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

type model struct {
	launcher              LauncherConfig
	cfg                   Config
	cfgPath               string
	env                   Environment
	discoveryGeneration   uint64
	cursor                int
	width                 int
	height                int
	loading               bool
	dirty                 bool
	confirmQuit           bool
	status                string
	statusErr             bool
	mode                  editMode
	input                 textinput.Model
	serverInput           textinput.Model
	area                  textarea.Model
	logView               viewport.Model
	memory                MemoryInfo
	showLog               bool
	launching             bool
	logText               string
	logPath               string
	launchErr             error
	launchCleanupErr      error
	diagnostics           []Diagnostic
	showAnalysis          bool
	activeSession         *launchSession
	consoleInput          bool
	serverStopPending     bool
	safeModeActive        bool
	showTools             bool
	toolsCursor           int
	toolBusy              bool
	toolStatus            string
	toolStatusErr         bool
	backupCount           int
	lastBackup            BackupResult
	showBackups           bool
	backups               []BackupInfo
	backupsCursor         int
	backupPreview         BackupPreview
	backupBusy            bool
	backupStatus          string
	backupStatusErr       bool
	confirmRestoreBackup  bool
	showPreflight         bool
	preflightBusy         bool
	preflight             PreflightReport
	showMods              bool
	mods                  []MindustryMod
	modsCursor            int
	modsStatus            string
	modsStatusErr         bool
	confirmDisableAll     bool
	modDisablePlan        MindustryModDisablePlan
	picking               bool
	pickerTarget          editMode
	pickerLabel           string
	picker                filepicker.Model
	showInstances         bool
	instancesCursor       int
	instanceEditAction    string
	confirmDeleteInstance bool
}

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	labelStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("111"))
)

func newModel(launcher LauncherConfig, cfgPath, initialStatus string, statusErr bool) model {
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
	memory := DetectMemory()
	result := model{
		launcher: launcher, cfg: cfg, cfgPath: cfgPath, loading: true,
		discoveryGeneration: 1, instancesCursor: launcherInstanceIndex(launcher, cfg.InstanceID),
		status: initialStatus, statusErr: statusErr,
		input: input, serverInput: serverInput, area: area, logView: logView, memory: memory,
	}
	result.loadLatestInstanceLog()
	return result
}

func (m *model) loadLatestInstanceLog() {
	path, output, err := latestLaunchLog(m.cfgPath, m.cfg.InstanceID)
	if err != nil {
		if m.status == "" {
			m.setStatus(err.Error(), true)
		}
		return
	}
	if path == "" {
		return
	}
	m.logPath = path
	m.logText = normalizeLog(output)
	m.logView.SetContent(m.logText)
	m.logView.GotoBottom()
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
	switch msg := msg.(type) {
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
		return m, nil
	case launchOutputMsg:
		if !m.launching {
			return m, nil
		}
		m.appendLog(msg.text)
		return m, waitLaunchOutput(m.activeSession)
	case launchStreamClosedMsg:
		return m, nil
	case launchFinishedMsg:
		wasServer := m.activeSession != nil && m.activeSession.spec.InteractiveConsole
		m.launching = false
		m.consoleInput = false
		m.serverStopPending = false
		m.serverInput.Blur()
		safeModeRestoreErr := error(nil)
		if m.safeModeActive {
			m.safeModeActive = false
			safeModeRestoreErr = EndMindustrySafeMode(
				resolveDataDirectory(m.cfg, m.cfgPath),
				safeModeStateDirectory(m.cfgPath, m.cfg.InstanceID),
			)
		}
		m.showLog = true
		m.logText = normalizeLog(msg.output)
		m.logPath = msg.logPath
		stopped := errors.Is(msg.err, ErrLaunchStopped)
		m.launchErr = msg.err
		m.launchCleanupErr = safeModeRestoreErr
		if stopped {
			m.launchErr = nil
			m.diagnostics = nil
		} else {
			m.diagnostics = AnalyzeLaunchFailure(m.logText, msg.err)
		}
		if safeModeRestoreErr != nil {
			m.appendLog("\n[启动器] 安全模式结束，但恢复模组失败：" + safeModeRestoreErr.Error() + "\n")
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
		return m, nil
	}
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "ctrl+c" && m.launching {
		m.showLog = true
		m.setStatus("游戏或服务器仍在运行；请先在游戏中退出，服务器可用 Ctrl+X 安全停止", true)
		return m, nil
	}

	if m.showLog {
		return m.updateLogView(msg)
	}
	if m.showPreflight {
		return m.updatePreflight(msg)
	}
	if m.showBackups {
		return m.updateBackups(msg)
	}
	if m.showMods {
		return m.updateMods(msg)
	}
	if m.showTools {
		return m.updateTools(msg)
	}
	if m.picking {
		return m.updatePicker(msg)
	}

	if m.mode != editNone {
		return m.updateEditor(msg)
	}
	if m.showInstances {
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
		if m.cursor == 0 {
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
		if m.cursor == 0 {
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
			preset := ResolveJVMPreset(presetAuto, m.memory)
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

func (m model) updateEditor(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.mode = editNone
			m.input.Blur()
			m.area.Blur()
			m.setStatus("已取消编辑", false)
			return m, nil
		case "enter":
			if m.mode == editInstanceName {
				return m.commitInstanceNameEditor()
			}
			if m.mode == editJavaPath || m.mode == editJarPath || m.mode == editWorkingDir || m.mode == editDataDir {
				m.commitPathEditor()
				return m, nil
			}
		case "ctrl+s":
			if m.mode == editJVMArgs || m.mode == editGameArgs {
				m.commitArgsEditor()
				return m, nil
			}
		}
	}
	var cmd tea.Cmd
	if m.mode == editJVMArgs || m.mode == editGameArgs {
		m.area, cmd = m.area.Update(msg)
	} else {
		m.input, cmd = m.input.Update(msg)
	}
	return m, cmd
}

func (m model) activate() (tea.Model, tea.Cmd) {
	if m.launching && m.cursor == 1 {
		m.setStatus("游戏进程仍在运行；可打开日志页面查看状态", true)
		return m, nil
	}
	switch m.cursor {
	case 0:
		m.showInstances = true
		m.instancesCursor = launcherInstanceIndex(m.launcher, m.cfg.InstanceID)
		m.confirmDeleteInstance = false
	case 1:
		return m.startConfiguredGame()
	case 2:
		return m, m.beginPathPicker(editJavaPath, "选择 Java 可执行文件")
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
		m.showTools = true
		m.refreshBackupCount()
	case 8:
		m.beginArgsEditor(editJVMArgs, "编辑 JVM 参数", m.cfg.JVMArgs)
	case 9:
		m.beginArgsEditor(editGameArgs, "编辑游戏参数", m.cfg.GameArgs)
	case 10:
		return m.startPreflight()
	case 11:
		if m.logText == "" {
			m.setStatus("还没有可查看的启动日志", true)
			return m, nil
		}
		m.showLog = true
		m.logView.SetContent(m.logDisplayContent())
		m.logView.GotoBottom()
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

func (m model) startConfiguredGame() (tea.Model, tea.Cmd) {
	if m.loading {
		m.setStatus("仍在检测 Java，请稍候", true)
		return m, nil
	}
	if recovered, err := recoverInstanceSafeMode(m.cfg, m.cfgPath); err != nil {
		m.setStatus("启动前无法完成安全模式恢复："+err.Error(), true)
		return m, nil
	} else if recovered {
		m.setStatus("已恢复上次中断的安全模式，正在继续启动", false)
	}
	m.syncActiveInstance()
	if err := saveLauncherConfig(m.cfgPath, m.launcher); err != nil {
		m.setStatus(err.Error(), true)
		return m, nil
	}
	m.dirty = false
	m.confirmQuit = false
	spec, err := prepareLaunch(m.cfg, m.cfgPath)
	if err != nil {
		m.setStatus(err.Error(), true)
		return m, nil
	}
	return m.startLaunchSpec(spec)
}

func (m model) startLaunchSpec(spec LaunchSpec) (tea.Model, tea.Cmd) {
	m.setStatus("正在启动: "+formatCommand(spec), false)
	session := newLaunchSession(spec, m.cfgPath)
	m.activeSession = session
	m.launching = true
	m.consoleInput = false
	m.serverStopPending = false
	m.showLog = true
	m.showTools = false
	m.showMods = false
	m.showBackups = false
	m.showPreflight = false
	m.launchErr = nil
	m.launchCleanupErr = nil
	m.diagnostics = nil
	m.showAnalysis = false
	m.logPath = session.writer.logPath()
	m.logText = ""
	m.logView.SetContent("")
	return m, tea.Batch(runLaunchSession(session), waitLaunchOutput(session))
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

func (m *model) beginPathEditor(mode editMode, label, value string) {
	m.mode = mode
	m.input.SetValue(value)
	m.input.Placeholder = label
	m.input.CursorEnd()
	m.input.Focus()
	m.setStatus(label+"；Enter 保存，Esc 取消", false)
}

func (m *model) beginPathPicker(target editMode, label string) tea.Cmd {
	picker := filepicker.New()
	picker.ShowHidden = true
	picker.ShowPermissions = false
	picker.ShowSize = true
	picker.AutoHeight = false
	picker.SetHeight(max(5, m.height-8))
	picker.DirAllowed = false
	picker.FileAllowed = target == editJavaPath || target == editJarPath
	if target == editJarPath {
		picker.AllowedTypes = []string{".jar"}
	}
	start := m.pathForPicker(target)
	picker.CurrentDirectory = nearestExistingDirectory(start, jarDirectory(m.cfg, m.cfgPath))
	m.picker = picker
	m.picking = true
	m.pickerTarget = target
	m.pickerLabel = label
	m.setStatus("Enter 进入目录/选择文件，S 选择当前目录，M 手动输入，C 清空，Esc 取消", false)
	return m.picker.Init()
}

func (m model) pathForPicker(target editMode) string {
	switch target {
	case editJavaPath:
		return resolveConfigPath(m.cfgPath, m.cfg.JavaPath)
	case editJarPath:
		return resolveConfigPath(m.cfgPath, m.cfg.JarPath)
	case editWorkingDir:
		return resolveConfigPath(m.cfgPath, m.cfg.WorkingDirectory)
	case editDataDir:
		return resolveDataDirectory(m.cfg, m.cfgPath)
	default:
		return ""
	}
}

func nearestExistingDirectory(path, fallback string) string {
	if path == "" {
		path = fallback
	}
	if info, err := filepath.EvalSymlinks(path); err == nil {
		path = info
	}
	if stat, err := os.Stat(path); err == nil && !stat.IsDir() {
		path = filepath.Dir(path)
	}
	for {
		if stat, err := os.Stat(path); err == nil && stat.IsDir() {
			if abs, absErr := filepath.Abs(path); absErr == nil {
				return abs
			}
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		path = parent
	}
	if abs, err := filepath.Abs(fallback); err == nil {
		return abs
	}
	return "."
}

func (m model) updatePicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.picking = false
			m.setStatus("已取消选择", false)
			return m, nil
		case "m":
			target := m.pickerTarget
			m.picking = false
			m.beginPathEditor(target, m.pickerLabel, m.pathConfigValue(target))
			return m, nil
		case "c":
			m.setSelectedPath(m.pickerTarget, "")
			m.picking = false
			return m, nil
		case "s":
			if m.pickerTarget == editWorkingDir || m.pickerTarget == editDataDir {
				m.setSelectedPath(m.pickerTarget, m.picker.CurrentDirectory)
				m.picking = false
				return m, nil
			}
		}
	}
	var cmd tea.Cmd
	m.picker, cmd = m.picker.Update(msg)
	if selected, path := m.picker.DidSelectFile(msg); selected {
		m.setSelectedPath(m.pickerTarget, path)
		m.picking = false
		return m, nil
	}
	return m, cmd
}

func (m model) pathConfigValue(target editMode) string {
	switch target {
	case editJavaPath:
		return m.cfg.JavaPath
	case editJarPath:
		return m.cfg.JarPath
	case editWorkingDir:
		return m.cfg.WorkingDirectory
	case editDataDir:
		return m.cfg.DataDirectory
	default:
		return ""
	}
}

func (m *model) setSelectedPath(target editMode, path string) {
	path = strings.TrimSpace(path)
	switch target {
	case editJavaPath:
		m.cfg.JavaPath = portablePath(m.cfgPath, path)
	case editJarPath:
		m.cfg.JarPath = portablePath(m.cfgPath, path)
	case editWorkingDir:
		m.cfg.WorkingDirectory = portablePath(m.cfgPath, path)
	case editDataDir:
		m.cfg.DataDirectory = portableDataDirectory(m.cfg, m.cfgPath, path)
	}
	m.dirty = true
	if target == editDataDir && path == "" {
		m.setStatus("已清空：将使用游戏自身默认数据目录，不添加专用启动参数", false)
	} else {
		m.setStatus("路径已更新；启动时会再次校验", false)
	}
}

func (m *model) beginArgsEditor(mode editMode, label string, args []string) {
	m.mode = mode
	m.area.SetValue(strings.Join(args, "\n"))
	m.area.Focus()
	m.setStatus(label+"；每行一个完整参数，Ctrl+S 保存，Esc 取消", false)
}

func (m *model) commitPathEditor() {
	value := strings.TrimSpace(m.input.Value())
	if m.mode == editDataDir {
		if value != "" && !filepath.IsAbs(value) {
			value = filepath.Join(jarDirectory(m.cfg, m.cfgPath), value)
		}
		m.setSelectedPath(m.mode, value)
		m.mode = editNone
		m.input.Blur()
		return
	}
	if value != "" {
		if abs, err := filepath.Abs(resolveConfigPath(m.cfgPath, value)); err == nil {
			value = portablePath(m.cfgPath, abs)
		}
	}
	switch m.mode {
	case editJavaPath:
		m.cfg.JavaPath = value
	case editJarPath:
		m.cfg.JarPath = value
	case editWorkingDir:
		m.cfg.WorkingDirectory = value
	case editDataDir:
		m.cfg.DataDirectory = value
	}
	m.mode = editNone
	m.input.Blur()
	m.dirty = true
	m.setStatus("已更新；启动时会再次校验", false)
}

func (m *model) commitArgsEditor() {
	args := argsFromLines(m.area.Value())
	if m.mode == editJVMArgs {
		m.cfg.JVMArgs = args
		m.cfg.JVMPreset = presetCustom
	} else {
		m.cfg.GameArgs = args
	}
	m.mode = editNone
	m.area.Blur()
	m.dirty = true
	m.setStatus("参数已更新", false)
}

func argsFromLines(value string) []string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	args := make([]string, 0, len(lines))
	for _, line := range lines {
		if arg := strings.TrimSpace(line); arg != "" {
			args = append(args, arg)
		}
	}
	return args
}

func (m *model) cycleJava(delta int) {
	required := selectedRequiredJava(m.cfg, m.cfgPath, m.env)
	valid := make([]JavaCandidate, 0, len(m.env.Java))
	for _, candidate := range m.env.Java {
		if candidate.Err == nil && (required == 0 || candidate.Version >= required) {
			valid = append(valid, candidate)
		}
	}
	if len(valid) == 0 {
		m.setStatus("没有可切换的兼容 Java", true)
		return
	}
	current := resolveConfigPath(m.cfgPath, m.cfg.JavaPath)
	index := 0
	for i, candidate := range valid {
		if pathKey(candidate.Path) == pathKey(current) {
			index = i
			break
		}
	}
	index = (index + delta + len(valid)) % len(valid)
	m.cfg.JavaPath = portablePath(m.cfgPath, valid[index].Path)
	m.dirty = true
	m.setStatus(fmt.Sprintf("已选择 Java %d（%s）", valid[index].Version, valid[index].Source), false)
}

func (m *model) cycleJar(delta int) {
	valid := make([]JarInfo, 0, len(m.env.Jars))
	for _, candidate := range m.env.Jars {
		if candidate.Err == nil {
			valid = append(valid, candidate)
		}
	}
	if len(valid) == 0 {
		m.setStatus("没有可切换的游戏 JAR", true)
		return
	}
	current := resolveConfigPath(m.cfgPath, m.cfg.JarPath)
	index := 0
	for i, candidate := range valid {
		if pathKey(candidate.Path) == pathKey(current) {
			index = i
			break
		}
	}
	index = (index + delta + len(valid)) % len(valid)
	m.cfg.JarPath = portablePath(m.cfgPath, valid[index].Path)
	m.dirty = true
	m.setStatus(fmt.Sprintf("已选择 %s（需要 Java %d+）", filepath.Base(valid[index].Path), valid[index].RequiredJavaVersion), false)
}

func (m *model) cycleProfile(delta int) (tea.Model, tea.Cmd) {
	profiles := configuredProfileIDs()
	current := m.cfg.GameProfile
	if current == "" {
		current = profileAuto
	}
	index := 0
	for i, profile := range profiles {
		if profile == current {
			index = i
			break
		}
	}
	index = (index + delta + len(profiles)) % len(profiles)
	m.cfg.GameProfile = profiles[index]
	m.dirty = true
	m.loading = true
	m.discoveryGeneration++
	m.setStatus("正在按新的游戏配置重新检查 JAR 与 Java…", false)
	return *m, discoverCmd(m.cfg, m.cfgPath, m.discoveryGeneration)
}

func (m *model) cycleJVMPreset(delta int) {
	presets := AvailableJVMPresets(m.memory)
	current := m.cfg.JVMPreset
	index := 0
	found := false
	for i, preset := range presets {
		if preset.ID == current {
			index = i
			found = true
			break
		}
	}
	if !found {
		index = 0
	} else {
		index = (index + delta + len(presets)) % len(presets)
	}
	preset := presets[index]
	m.cfg.JVMPreset = preset.ID
	m.cfg.JVMArgs = preset.Args
	m.dirty = true
	m.setStatus("已选择 "+preset.Name+"："+preset.Description, false)
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

func (m model) View() string {
	if m.showLog {
		return m.logViewPage()
	}
	if m.showPreflight {
		return m.preflightView()
	}
	if m.showBackups {
		return m.backupsView()
	}
	if m.showMods {
		return m.modsView()
	}
	if m.showTools {
		return m.toolsView()
	}
	if m.mode != editNone {
		return m.editorView()
	}
	if m.picking {
		return m.pickerView()
	}
	if m.showInstances {
		return m.instancesView()
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Mindustry-first Java 游戏启动器"))
	if m.dirty {
		b.WriteString(dimStyle.Render("  [配置未保存]"))
	}
	b.WriteString("\n\n")
	b.WriteString(m.summaryView())
	b.WriteString("\n\n")
	items := []struct{ label, value string }{
		{"实例", activeInstanceDisplay(m.launcher)},
		{"启动游戏", ""},
		{"Java 路径", shortPath(m.cfg.JavaPath, m.width-28)},
		{"游戏 JAR", shortPath(m.cfg.JarPath, m.width-28)},
		{"游戏配置", profileDisplayForModel(m)},
		{"工作目录", displayDefault(m.cfg.WorkingDirectory, "自动：JAR 所在目录")},
		{"数据目录", dataDirectoryDisplay(m, m.width-28)},
		{"Mindustry 工具", "备份/恢复 · 模组 · 安全启动"},
		{"JVM 参数", fmt.Sprintf("%s · %d 项", jvmPresetDisplay(m.cfg, m.memory), len(m.cfg.JVMArgs))},
		{"游戏参数", fmt.Sprintf("%d 项", len(m.cfg.GameArgs))},
		{"启动前检查", "Java · 模块 · 参数 · 目录 · 图形会话"},
		{"查看上次日志", logFileName(m.logPath)},
		{"重新检测", ""},
		{"保存配置", ""},
		{"退出", ""},
	}
	for i, item := range items {
		cursor := "  "
		style := lipgloss.NewStyle()
		if i == m.cursor {
			cursor = "› "
			style = selectedStyle
		}
		line := cursor + item.label
		if item.value != "" {
			line += "  " + dimStyle.Render(item.value)
		}
		b.WriteString(style.Render(line))
		b.WriteByte('\n')
	}
	b.WriteString("\n")
	if m.status != "" {
		style := okStyle
		if m.statusErr {
			style = errStyle
		}
		b.WriteString(style.Render(clampText(m.status, max(30, m.width-2))))
		b.WriteByte('\n')
	}
	b.WriteString(dimStyle.Render("↑/↓ 选择  Enter 编辑/执行  ←/→ 切换  D 恢复默认参数  S 保存  R 检测  Q 退出"))
	return b.String()
}

func (m model) summaryView() string {
	javaText := "未找到"
	if java, ok := findSelectedJava(m.cfg, m.cfgPath, m.env); ok {
		if java.Err != nil {
			javaText = "不可用（" + java.Err.Error() + "）"
		} else {
			arch := java.Architecture
			if java.DataModel > 0 {
				arch = fmt.Sprintf("%s/%d位", displayDefault(arch, "未知架构"), java.DataModel)
			}
			javaText = fmt.Sprintf("Java %d · %s · %s", java.Version, arch, java.Source)
		}
	} else if m.loading {
		javaText = "检测中…"
	}
	jarText := "未找到"
	if jar, ok := findSelectedJar(m.cfg, m.cfgPath, m.env); ok {
		if jar.Err != nil {
			jarText = "不可用（" + jar.Err.Error() + "）"
		} else {
			jarText = fmt.Sprintf("%s · 需要 Java %d+", filepath.Base(jar.Path), jar.RequiredJavaVersion)
		}
	} else if m.loading {
		jarText = "检测中…"
	}
	return labelStyle.Render("运行时  ") + javaText + "\n" +
		labelStyle.Render("游戏    ") + jarText + "\n" +
		labelStyle.Render("适配器  ") + profileDisplayForModel(m) + "\n" +
		labelStyle.Render("配置文件") + "  " + shortPath(m.cfgPath, m.width-12)
}

func (m model) updateLogView(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.consoleInput {
		if key, ok := msg.(tea.KeyMsg); ok {
			switch key.String() {
			case "esc":
				m.consoleInput = false
				m.serverInput.Blur()
				return m, nil
			case "enter":
				command := strings.TrimSpace(m.serverInput.Value())
				if command != "" {
					if err := m.activeSession.SendInput(command); err != nil {
						m.setStatus("发送服务器命令失败："+err.Error(), true)
					} else {
						m.appendLog("[控制台] > " + command + "\n")
					}
				}
				m.serverInput.SetValue("")
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.serverInput, cmd = m.serverInput.Update(msg)
		return m, cmd
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc", "q":
			m.showLog = false
			return m, nil
		case "g", "home":
			m.logView.GotoTop()
			return m, nil
		case "G", "end":
			m.logView.GotoBottom()
			return m, nil
		case "d":
			if len(m.diagnostics) > 0 {
				m.showAnalysis = !m.showAnalysis
				m.logView.SetContent(m.logDisplayContent())
				m.logView.GotoTop()
			}
			return m, nil
		case "i":
			if m.launching && m.activeSession != nil && m.activeSession.spec.InteractiveConsole {
				m.consoleInput = true
				m.serverInput.Focus()
				return m, textinput.Blink
			}
		case "ctrl+x":
			if m.launching && m.activeSession != nil && m.activeSession.spec.InteractiveConsole {
				if !m.serverStopPending {
					if err := m.activeSession.SendInput("exit"); err != nil {
						m.setStatus("请求服务器安全退出失败："+err.Error(), true)
					} else {
						m.serverStopPending = true
						m.appendLog("\n[启动器] 已发送 exit，请等待服务器保存并退出；再次按 Ctrl+X 可强制终止。\n")
					}
				} else if err := m.activeSession.Stop(); err != nil {
					m.setStatus("强制停止服务器失败："+err.Error(), true)
				} else {
					m.appendLog("\n[启动器] 已强制终止服务器进程。\n")
				}
				return m, nil
			}
		}
	}
	var cmd tea.Cmd
	m.logView, cmd = m.logView.Update(msg)
	return m, cmd
}

func (m *model) appendLog(text string) {
	m.logText += normalizeLog(text)
	if len(m.logText) > maxCapturedLogBytes {
		m.logText = "[启动器] TUI 仅保留最后 4 MiB；完整内容请查看日志文件。\n\n" +
			m.logText[len(m.logText)-maxCapturedLogBytes:]
	}
	m.logView.SetContent(m.logDisplayContent())
	m.logView.GotoBottom()
}

func normalizeLog(text string) string {
	text = ansi.Strip(text)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Map(func(character rune) rune {
		if (character < 0x20 && character != '\n' && character != '\t') || character == 0x7f {
			return -1
		}
		return character
	}, text)
}

func (m model) logViewPage() string {
	title := "启动日志"
	state := okStyle.Render("游戏已退出")
	if m.activeSession != nil && m.activeSession.spec.InteractiveConsole {
		title = "服务器日志与控制台"
		state = okStyle.Render("服务器已退出")
	}
	if m.launching {
		if m.activeSession != nil && m.activeSession.spec.InteractiveConsole {
			state = selectedStyle.Render("服务器正在运行，日志实时更新中…")
		} else {
			state = selectedStyle.Render("游戏正在运行，日志实时更新中…")
		}
	} else if m.launchCleanupErr != nil {
		state = errStyle.Render("退出后恢复失败：" + m.launchCleanupErr.Error())
	} else if m.launchErr != nil {
		state = errStyle.Render("启动失败：" + m.launchErr.Error())
	}
	path := m.logPath
	if path == "" {
		path = "日志文件不可写，仅保留当前 TUI 内容"
	}
	percent := int(m.logView.ScrollPercent() * 100)
	diagnosticStatus := ""
	if len(m.diagnostics) > 0 {
		diagnosticStatus = fmt.Sprintf(" · %d 条诊断", len(m.diagnostics))
	}
	console := ""
	footer := "↑/↓/PgUp/PgDn 滚动  g/G 顶部/底部  D 切换诊断  Esc 返回"
	if m.launching && m.activeSession != nil && m.activeSession.spec.InteractiveConsole {
		stopHelp := "Ctrl+X 安全停止"
		if m.serverStopPending {
			stopHelp = "Ctrl+X 强制停止"
		}
		footer += "  I 输入命令  " + stopHelp
		if m.consoleInput {
			console = "\n" + m.serverInput.View()
			footer = "Enter 发送命令  Esc 取消输入"
		}
	}
	return titleStyle.Render(title) + "  " + state + diagnosticStatus + "\n" +
		dimStyle.Render("文件: "+shortPath(path, m.width-8)) + "\n\n" +
		m.logView.View() + console + "\n" +
		dimStyle.Render(fmt.Sprintf("%s  %d%%", footer, percent))
}

func (m model) editorView() string {
	var title, help, body string
	switch m.mode {
	case editJavaPath:
		title, help, body = "Java 可执行文件路径", "Enter 保存 · Esc 取消", m.input.View()
	case editJarPath:
		title, help, body = "可执行游戏 JAR 路径", "Enter 保存 · Esc 取消", m.input.View()
	case editWorkingDir:
		title, help, body = "工作目录", "留空表示 JAR 所在目录 · Enter 保存 · Esc 取消", m.input.View()
	case editDataDir:
		title, help, body = "游戏数据目录", "留空表示游戏自身默认目录（不添加专用 -D 参数）· Enter 保存 · Esc 取消", m.input.View()
	case editJVMArgs:
		title, help, body = "JVM 参数", "每行一个完整参数 · Ctrl+S 保存 · Esc 取消", m.area.View()
	case editGameArgs:
		title, help, body = "游戏参数", "每行一个完整参数 · Ctrl+S 保存 · Esc 取消", m.area.View()
	case editInstanceName:
		title, help, body = instanceEditorTitle(m.instanceEditAction), "Enter 保存 · Esc 取消", m.input.View()
	}
	return titleStyle.Render(title) + "\n\n" + body + "\n\n" + dimStyle.Render(help)
}

func (m model) pickerView() string {
	kind := "文件"
	selectHelp := "Enter 进入目录/选择文件"
	if m.pickerTarget == editWorkingDir || m.pickerTarget == editDataDir {
		kind = "目录"
		selectHelp = "Enter 进入目录 · S 选择当前目录"
	}
	return titleStyle.Render(m.pickerLabel) + "\n" +
		labelStyle.Render("当前目录  ") + shortPath(m.picker.CurrentDirectory, m.width-12) + "\n\n" +
		m.picker.View() + "\n" +
		dimStyle.Render(selectHelp+" · M 手动输入 · C 清空 · ←/Backspace 返回上级 · Esc 取消 · 类型 "+kind)
}

func dataDirectoryDisplay(m model, width int) string {
	adapter := effectiveAdapterForModel(m)
	if adapter.DataDirectoryProperty() == "" {
		return "当前配置不使用专用数据目录"
	}
	if m.cfg.DataDirectory == "" {
		return "游戏默认（不传参数）"
	}
	return shortPath(m.cfg.DataDirectory+" → "+resolveDataDirectory(m.cfg, m.cfgPath), width)
}

func effectiveAdapterForModel(m model) GameAdapter {
	if jar, ok := findSelectedJar(m.cfg, m.cfgPath, m.env); ok {
		return effectiveAdapter(m.cfg, jar)
	}
	return resolveGameAdapter(m.cfg.GameProfile, "")
}

func profileDisplayForModel(m model) string {
	mainClass := ""
	if jar, ok := findSelectedJar(m.cfg, m.cfgPath, m.env); ok {
		mainClass = jar.MainClass
	}
	return profileDisplayName(m.cfg.GameProfile, mainClass)
}

func applyAutoSelections(cfg *Config, cfgPath string, env Environment) bool {
	changed := false
	jar, jarOK := findSelectedJar(*cfg, cfgPath, env)
	if !jarOK || jar.Err != nil {
		if candidate, ok := selectJar(env.Jars); ok {
			cfg.JarPath = portablePath(cfgPath, candidate.Path)
			jar, jarOK = candidate, true
			changed = true
		}
	}
	required := 0
	if jarOK && jar.Err == nil {
		required = jar.RequiredJavaVersion
	}
	java, javaOK := findSelectedJava(*cfg, cfgPath, env)
	if !javaOK || java.Err != nil || (required > 0 && java.Version < required) {
		if candidate, ok := selectJava(env.Java, required); ok {
			cfg.JavaPath = portablePath(cfgPath, candidate.Path)
			changed = true
		}
	}
	return changed
}

func selectedJava(cfg Config, cfgPath string, env Environment) bool {
	java, ok := findSelectedJava(cfg, cfgPath, env)
	return ok && java.Err == nil && java.Version >= selectedRequiredJava(cfg, cfgPath, env)
}

func selectedJar(cfg Config, cfgPath string, env Environment) bool {
	jar, ok := findSelectedJar(cfg, cfgPath, env)
	return ok && jar.Err == nil
}

func selectedRequiredJava(cfg Config, cfgPath string, env Environment) int {
	jar, ok := findSelectedJar(cfg, cfgPath, env)
	if ok && jar.Err == nil {
		return jar.RequiredJavaVersion
	}
	return 0
}

func findSelectedJava(cfg Config, cfgPath string, env Environment) (JavaCandidate, bool) {
	current := resolveConfigPath(cfgPath, cfg.JavaPath)
	if cfg.JavaPath == "" {
		return JavaCandidate{}, false
	}
	for _, candidate := range env.Java {
		if pathKey(candidate.Path) == pathKey(current) {
			return candidate, true
		}
	}
	return JavaCandidate{}, false
}

func findSelectedJar(cfg Config, cfgPath string, env Environment) (JarInfo, bool) {
	current := resolveConfigPath(cfgPath, cfg.JarPath)
	if cfg.JarPath == "" {
		return JarInfo{}, false
	}
	for _, candidate := range env.Jars {
		if pathKey(candidate.Path) == pathKey(current) {
			return candidate, true
		}
	}
	return JarInfo{}, false
}

func shortPath(value string, width int) string {
	if value == "" {
		return "未设置"
	}
	return clampText(value, max(12, width))
}

func clampText(value string, width int) string {
	if lipgloss.Width(value) <= width {
		return value
	}
	runes := []rune(value)
	for len(runes) > 0 && lipgloss.Width("…"+string(runes)) > width {
		runes = runes[1:]
	}
	return "…" + string(runes)
}

func displayDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func logFileName(path string) string {
	if path == "" {
		return "暂无"
	}
	return filepath.Base(path)
}

func jvmPresetDisplay(cfg Config, memory MemoryInfo) string {
	if cfg.JVMPreset == "" || cfg.JVMPreset == presetCustom {
		return "自定义"
	}
	return ResolveJVMPreset(cfg.JVMPreset, memory).Name
}
