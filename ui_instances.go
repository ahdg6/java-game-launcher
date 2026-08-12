package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	instanceEditNew    = "new"
	instanceEditClone  = "clone"
	instanceEditRename = "rename"
)

func launcherInstanceIndex(launcher LauncherConfig, id string) int {
	for index := range launcher.Instances {
		if launcher.Instances[index].ID == id {
			return index
		}
	}
	return 0
}

func activeInstanceDisplay(launcher LauncherConfig) string {
	index := launcherInstanceIndex(launcher, launcher.ActiveInstanceID)
	name := launcher.ActiveInstanceID
	if index >= 0 && index < len(launcher.Instances) {
		name = launcher.Instances[index].Name
	}
	return fmt.Sprintf("%s  [%d/%d]", name, index+1, len(launcher.Instances))
}

func (m *model) syncActiveInstance() {
	instance := m.launcher.InstanceByID(m.cfg.InstanceID)
	if instance == nil {
		instance, _ = m.launcher.Active()
	}
	if instance == nil {
		return
	}
	instance.ApplyConfig(m.cfg)
	m.launcher.ActiveInstanceID = instance.ID
}

func (m model) switchInstanceBy(delta int) (tea.Model, tea.Cmd) {
	if len(m.launcher.Instances) < 2 {
		m.setStatus("当前只有一个实例；按 Enter 进入实例管理可新建或克隆", false)
		return m, nil
	}
	index := launcherInstanceIndex(m.launcher, m.cfg.InstanceID)
	index = (index + delta + len(m.launcher.Instances)) % len(m.launcher.Instances)
	return m.selectInstanceAt(index, false)
}

func (m model) selectInstanceAt(index int, keepManager bool) (tea.Model, tea.Cmd) {
	if m.launching {
		m.setStatus("游戏或服务器运行时不能切换实例", true)
		return m, nil
	}
	if index < 0 || index >= len(m.launcher.Instances) {
		m.setStatus("实例选择超出范围", true)
		return m, nil
	}
	m.syncActiveInstance()
	target := &m.launcher.Instances[index]
	m.launcher.ActiveInstanceID = target.ID
	m.cfg = target.Config()
	m.instancesCursor = index
	m.showInstances = keepManager
	m.confirmDeleteInstance = false
	m.env = Environment{}
	m.loading = true
	m.discoveryGeneration++
	m.showLog = false
	m.showHistory = false
	m.historyLogs = nil
	m.showZulu = false
	m.zuluPackage = ZuluPackage{}
	m.logText = ""
	m.logPath = ""
	m.launchErr = nil
	m.historyLogFailed = false
	m.launchCleanupErr = nil
	m.diagnostics = nil
	m.showAnalysis = false
	m.showPreflight = false
	m.preflight = PreflightReport{}
	m.activeSession = nil
	m.mods = nil
	m.backups = nil
	m.backupPreview = BackupPreview{}
	m.backupStatus = ""
	m.confirmRestoreBackup = false
	m.modDisablePlan = MindustryModDisablePlan{}
	m.toolStatus = ""
	m.modsStatus = ""
	m.backupCount = 0
	m.lastBackup = BackupResult{}
	m.loadLatestInstanceLog()
	m.dirty = true
	status := "已切换到实例 " + target.Name + "，正在重新检测 Java 与游戏 JAR…"
	if shared := m.sharedDataDirectoryNames(target.ID); len(shared) > 0 {
		status += "；警告：与 " + strings.Join(shared, "、") + " 共用数据目录"
		m.statusErr = true
	} else {
		m.statusErr = false
	}
	m.status = status
	return m, discoverCmd(m.cfg, m.cfgPath, m.discoveryGeneration)
}

func (m model) updateInstances(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.showInstances = false
		m.confirmDeleteInstance = false
		return m, nil
	case "up", "k":
		m.instancesCursor = (m.instancesCursor - 1 + len(m.launcher.Instances)) % len(m.launcher.Instances)
		m.confirmDeleteInstance = false
	case "down", "j":
		m.instancesCursor = (m.instancesCursor + 1) % len(m.launcher.Instances)
		m.confirmDeleteInstance = false
	case "enter", " ":
		return m.selectInstanceAt(m.instancesCursor, false)
	case "n":
		m.beginInstanceNameEditor(instanceEditNew, "新实例")
		return m, nil
	case "c":
		name := m.launcher.Instances[m.instancesCursor].Name + " 副本"
		m.beginInstanceNameEditor(instanceEditClone, name)
		return m, nil
	case "r":
		m.beginInstanceNameEditor(instanceEditRename, m.launcher.Instances[m.instancesCursor].Name)
		return m, nil
	case "d":
		if len(m.launcher.Instances) <= 1 {
			m.setStatus("不能删除最后一个实例", true)
			return m, nil
		}
		selected := m.launcher.Instances[m.instancesCursor]
		pending, pendingErr := IsMindustrySafeModePending(safeModeStateDirectory(m.cfgPath, selected.ID))
		if pendingErr != nil || pending {
			message := "该实例仍有安全模式恢复任务，恢复模组后才能删除"
			if pendingErr != nil {
				message += "：" + pendingErr.Error()
			}
			m.setStatus(message, true)
			return m, nil
		}
		if !m.confirmDeleteInstance {
			m.confirmDeleteInstance = true
			m.setStatus("再次按 D 确认删除实例 “"+selected.Name+"”；不会删除其游戏数据", true)
			return m, nil
		}
		m.syncActiveInstance()
		wasActive := selected.ID == m.cfg.InstanceID
		if err := m.launcher.DeleteInstance(selected.ID); err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.confirmDeleteInstance = false
		m.instancesCursor = min(m.instancesCursor, len(m.launcher.Instances)-1)
		m.dirty = true
		if wasActive {
			active, _ := m.launcher.Active()
			m.cfg = active.Config()
			return m.selectInstanceAt(launcherInstanceIndex(m.launcher, m.launcher.ActiveInstanceID), true)
		}
		m.setStatus("已删除实例配置；游戏数据未删除", false)
	case "J":
		return m.moveSelectedInstance(1)
	case "K":
		return m.moveSelectedInstance(-1)
	}
	return m, nil
}

func (m *model) beginInstanceNameEditor(action, value string) {
	m.instanceEditAction = action
	m.mode = editInstanceName
	m.input.SetValue(value)
	m.input.Placeholder = "实例显示名称"
	m.input.CursorEnd()
	m.input.Focus()
	m.setStatus(instanceEditorTitle(action)+"；实例 ID 会自动生成且重命名时保持不变", false)
}

func instanceEditorTitle(action string) string {
	switch action {
	case instanceEditNew:
		return "新建实例"
	case instanceEditClone:
		return "克隆实例"
	case instanceEditRename:
		return "重命名实例"
	default:
		return "实例名称"
	}
}

func (m model) commitInstanceNameEditor() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.input.Value())
	if name == "" {
		m.setStatus("实例名称不能为空", true)
		return m, nil
	}
	m.mode = editNone
	m.input.Blur()
	selected := m.launcher.Instances[m.instancesCursor]
	var created *InstanceConfig
	var err error
	switch m.instanceEditAction {
	case instanceEditNew:
		id := uniqueInstanceID(m.launcher, instanceIDFromName(name))
		created, err = m.launcher.CreateInstance(id, name)
	case instanceEditClone:
		id := uniqueInstanceID(m.launcher, selected.ID+"-copy")
		created, err = m.launcher.CloneInstance(selected.ID, id, name)
	case instanceEditRename:
		err = m.launcher.RenameInstance(selected.ID, name)
	default:
		err = fmt.Errorf("未知实例编辑操作 %q", m.instanceEditAction)
	}
	if err != nil {
		m.setStatus(err.Error(), true)
		return m, nil
	}
	m.dirty = true
	if created != nil {
		index := launcherInstanceIndex(m.launcher, created.ID)
		return m.selectInstanceAt(index, true)
	}
	m.setStatus("实例已重命名；稳定 ID 保持为 "+selected.ID, false)
	return m, nil
}

func (m model) moveSelectedInstance(offset int) (tea.Model, tea.Cmd) {
	selected := m.launcher.Instances[m.instancesCursor]
	if err := m.launcher.MoveInstance(selected.ID, offset); err != nil {
		m.setStatus(err.Error(), true)
		return m, nil
	}
	m.instancesCursor = launcherInstanceIndex(m.launcher, selected.ID)
	m.dirty = true
	m.confirmDeleteInstance = false
	m.setStatus("实例顺序已调整", false)
	return m, nil
}

func (m model) instancesView() string {
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("启动实例管理") + "\n\n")
	for index, instance := range m.launcher.Instances {
		cursor := "  "
		style := dimStyle
		if index == m.instancesCursor {
			cursor = "› "
			style = selectedStyle
		}
		active := ""
		if instance.ID == m.cfg.InstanceID {
			active = "  [当前]"
		}
		shared := ""
		if names := m.sharedDataDirectoryNames(instance.ID); len(names) > 0 {
			shared = "  ⚠ 与 " + strings.Join(names, "、") + " 共用数据"
		}
		line := fmt.Sprintf("%s%s  (%s)%s%s", cursor, instance.Name, instance.ID, active, shared)
		builder.WriteString(style.Render(clampText(line, max(30, m.width-2))) + "\n")
		if index == m.instancesCursor {
			cfg := instance.Config()
			builder.WriteString(dimStyle.Render("    JAR "+displayDefault(cfg.JarPath, "自动发现")+" · 数据 "+displayDefault(cfg.DataDirectory, "游戏默认")) + "\n")
		}
	}
	if m.status != "" {
		style := okStyle
		if m.statusErr {
			style = errStyle
		}
		builder.WriteString("\n" + style.Render(clampText(m.status, max(30, m.width-2))) + "\n")
	}
	builder.WriteString("\n" + dimStyle.Render("↑/↓ 选择  Enter 使用  N 新建  C 克隆  R 重命名  D×2 删除  Shift+J/K 排序  Esc 返回"))
	return builder.String()
}

func instanceIDFromName(name string) string {
	var builder strings.Builder
	previousDash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			previousDash = false
		case r == '-' || r == '_' || unicode.IsSpace(r):
			if builder.Len() > 0 && !previousDash {
				builder.WriteByte('-')
				previousDash = true
			}
		}
		if builder.Len() >= 48 {
			break
		}
	}
	id := strings.Trim(builder.String(), "-_")
	if id == "" || !instanceIDPattern.MatchString(id) {
		return "instance"
	}
	return id
}

func uniqueInstanceID(launcher LauncherConfig, base string) string {
	base = strings.Trim(base, "-_")
	if base == "" || !instanceIDPattern.MatchString(base) {
		base = "instance"
	}
	if len(base) > 56 {
		base = strings.Trim(base[:56], "-_")
	}
	if launcher.InstanceByID(base) == nil {
		return base
	}
	for number := 2; ; number++ {
		candidate := fmt.Sprintf("%s-%d", base, number)
		if launcher.InstanceByID(candidate) == nil {
			return candidate
		}
	}
}

func (m model) sharedDataDirectoryNames(instanceID string) []string {
	selected := m.launcher.InstanceByID(instanceID)
	if selected == nil {
		return nil
	}
	selectedPath := resolveDataDirectory(selected.Config(), m.cfgPath)
	if selectedPath == "" {
		return nil
	}
	names := make([]string, 0, 2)
	for index := range m.launcher.Instances {
		candidate := &m.launcher.Instances[index]
		if candidate.ID == selected.ID {
			continue
		}
		candidatePath := resolveDataDirectory(candidate.Config(), m.cfgPath)
		if candidatePath != "" && pathKey(filepath.Clean(candidatePath)) == pathKey(filepath.Clean(selectedPath)) {
			names = append(names, candidate.Name)
		}
	}
	return names
}
