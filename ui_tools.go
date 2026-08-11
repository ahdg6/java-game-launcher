package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const mindustryToolCount = 6

type toolResultMsg struct {
	message string
	err     error
	backup  *BackupResult
}

func (m model) updateTools(msg tea.Msg) (tea.Model, tea.Cmd) {
	if result, ok := msg.(toolResultMsg); ok {
		m.toolBusy = false
		m.toolStatusErr = result.err != nil
		if result.err != nil {
			m.toolStatus = result.err.Error()
		} else {
			m.toolStatus = result.message
		}
		if result.backup != nil {
			m.lastBackup = *result.backup
			m.refreshBackupCount()
		}
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		if !m.toolBusy {
			m.showTools = false
		}
	case "up", "k":
		m.toolsCursor = (m.toolsCursor - 1 + mindustryToolCount) % mindustryToolCount
	case "down", "j":
		m.toolsCursor = (m.toolsCursor + 1) % mindustryToolCount
	case "enter", " ":
		return m.activateTool()
	}
	return m, nil
}

func (m model) activateTool() (tea.Model, tea.Cmd) {
	if m.toolBusy {
		return m, nil
	}
	dataDir := resolveDataDirectory(m.cfg, m.cfgPath)
	switch m.toolsCursor {
	case 0:
		if m.launching {
			m.toolStatus, m.toolStatusErr = "游戏运行中不会创建备份，请退出游戏后重试", true
			return m, nil
		}
		if dataDir == "" {
			m.toolStatus, m.toolStatusErr = "数据目录已清空，启动器不知道要备份哪个位置", true
			return m, nil
		}
		if _, err := os.Stat(dataDir); err != nil {
			m.toolStatus, m.toolStatusErr = "数据目录尚不存在或不可读："+err.Error(), true
			return m, nil
		}
		backupDir := m.backupDirectory()
		m.toolBusy = true
		m.toolStatus, m.toolStatusErr = "正在创建安全备份…", false
		return m, func() tea.Msg {
			result, err := CreateDataBackup(dataDir, backupDir)
			if err != nil {
				return toolResultMsg{err: err}
			}
			message := fmt.Sprintf("备份完成：%s（%d 个文件，%s）", filepath.Base(result.Path), result.FileCount, humanBytes(result.ArchiveBytes))
			return toolResultMsg{message: message, backup: &result}
		}
	case 1:
		return m.openToolDirectory(dataDir, "数据目录")
	case 2:
		if dataDir == "" {
			m.toolStatus, m.toolStatusErr = "请先配置数据目录", true
			return m, nil
		}
		return m.openToolDirectory(filepath.Join(dataDir, "mods"), "模组目录")
	case 3:
		m.showMods = true
		m.refreshMods()
	case 4:
		return m.openToolDirectory(m.backupDirectory(), "备份目录")
	case 5:
		m.showTools = false
	}
	return m, nil
}

func (m model) openToolDirectory(path, label string) (tea.Model, tea.Cmd) {
	if path == "" {
		m.toolStatus, m.toolStatusErr = label+"尚未配置", true
		return m, nil
	}
	m.toolBusy = true
	m.toolStatus, m.toolStatusErr = "正在打开"+label+"…", false
	return m, func() tea.Msg {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return toolResultMsg{err: fmt.Errorf("创建%s: %w", label, err)}
		}
		if err := openPath(path); err != nil {
			return toolResultMsg{err: err}
		}
		return toolResultMsg{message: "已打开" + label}
	}
}

func (m *model) refreshBackupCount() {
	backups, err := ListBackups(m.backupDirectory())
	if err != nil {
		m.toolStatus, m.toolStatusErr = err.Error(), true
		return
	}
	m.backupCount = len(backups)
}

func (m model) backupDirectory() string {
	return filepath.Join(jarDirectory(m.cfg, m.cfgPath), "backups")
}

func (m model) toolsView() string {
	dataDir := resolveDataDirectory(m.cfg, m.cfgPath)
	modsDir := ""
	if dataDir != "" {
		modsDir = filepath.Join(dataDir, "mods")
	}
	items := []struct{ label, value string }{
		{"立即备份数据", fmt.Sprintf("已有 %d 个备份", m.backupCount)},
		{"打开数据目录", shortPath(dataDir, m.width-30)},
		{"打开模组目录", shortPath(modsDir, m.width-30)},
		{"管理模组", "安全启用/禁用 · 支持批量恢复"},
		{"打开备份目录", shortPath(m.backupDirectory(), m.width-30)},
		{"返回主菜单", ""},
	}
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("Mindustry 工具"))
	if m.toolBusy {
		builder.WriteString(selectedStyle.Render("  [处理中]"))
	}
	builder.WriteString("\n\n")
	builder.WriteString(labelStyle.Render("数据目录  ") + displayDefault(dataDir, "游戏默认（启动器未知）") + "\n")
	builder.WriteString(labelStyle.Render("备份策略  ") + "保留存档/设置/模组/地图，排除 cache/tmp，跳过符号链接\n\n")
	for index, item := range items {
		cursor := "  "
		style := dimStyle
		if index == m.toolsCursor {
			cursor = "› "
			style = selectedStyle
		}
		line := cursor + item.label
		if item.value != "" {
			line += "  " + item.value
		}
		builder.WriteString(style.Render(line) + "\n")
	}
	if m.lastBackup.Path != "" {
		builder.WriteString("\n" + okStyle.Render("最近备份  "+m.lastBackup.Path) + "\n")
	}
	if m.toolStatus != "" {
		style := okStyle
		if m.toolStatusErr {
			style = errStyle
		}
		builder.WriteString("\n" + style.Render(clampText(m.toolStatus, max(30, m.width-2))) + "\n")
	}
	builder.WriteString("\n" + dimStyle.Render("↑/↓ 选择  Enter 执行  Esc 返回；游戏运行时禁止备份"))
	return builder.String()
}

func humanBytes(size int64) string {
	const unit = int64(1024)
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	for _, suffix := range units {
		value /= 1024
		if value < 1024 || suffix == units[len(units)-1] {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%d B", size)
}
