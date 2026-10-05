package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/filepicker"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type editorState struct {
	mode         editMode
	input        textinput.Model
	area         textarea.Model
	picking      bool
	pickerTarget editMode
	pickerLabel  string
	picker       filepicker.Model
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
