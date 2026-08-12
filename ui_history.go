package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *model) refreshHistory() {
	logs, err := listLaunchLogs(m.cfgPath, m.cfg.InstanceID)
	if err != nil {
		m.historyLogs = nil
		m.historyStatus, m.historyStatusErr = err.Error(), true
		return
	}
	m.historyLogs = logs
	if len(logs) == 0 {
		m.historyCursor = 0
		m.historyStatus, m.historyStatusErr = "当前实例还没有启动日志", false
		return
	}
	if m.historyCursor >= len(logs) {
		m.historyCursor = len(logs) - 1
	}
	m.historyStatus, m.historyStatusErr = fmt.Sprintf("当前实例共有 %d 份启动日志", len(logs)), false
}

func (m model) updateHistory(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.showHistory = false
		m.confirmDeleteLog = false
		return m, nil
	case "up", "k":
		if len(m.historyLogs) > 0 {
			m.historyCursor = (m.historyCursor - 1 + len(m.historyLogs)) % len(m.historyLogs)
		}
		m.confirmDeleteLog = false
	case "down", "j":
		if len(m.historyLogs) > 0 {
			m.historyCursor = (m.historyCursor + 1) % len(m.historyLogs)
		}
		m.confirmDeleteLog = false
	case "enter", " ":
		return m.openSelectedHistoryLog()
	case "r":
		m.refreshHistory()
		m.confirmDeleteLog = false
	case "d":
		if m.launching {
			m.confirmDeleteLog = false
			m.historyStatus, m.historyStatusErr = "游戏运行期间不能删除启动日志", true
			return m, nil
		}
		if len(m.historyLogs) == 0 || m.historyCursor >= len(m.historyLogs) {
			m.historyStatus, m.historyStatusErr = "没有可删除的启动日志", true
			return m, nil
		}
		selected := m.historyLogs[m.historyCursor]
		if !m.confirmDeleteLog {
			m.confirmDeleteLog = true
			m.historyStatus, m.historyStatusErr = "再次按 D 确认删除 "+selected.Name, true
			return m, nil
		}
		if err := deleteLaunchLog(selected.Path); err != nil {
			m.historyStatus, m.historyStatusErr = err.Error(), true
			return m, nil
		}
		if selected.Path == m.logPath {
			m.logPath, m.logText = "", ""
			m.diagnostics = nil
			m.launchErr = nil
			m.historyLogFailed = false
		}
		m.confirmDeleteLog = false
		m.refreshHistory()
		m.historyStatus, m.historyStatusErr = "日志已删除", false
	}
	return m, nil
}

func (m model) openSelectedHistoryLog() (tea.Model, tea.Cmd) {
	if m.launching {
		m.historyStatus, m.historyStatusErr = "游戏运行期间不能切换日志；请返回实时日志页", true
		return m, nil
	}
	if len(m.historyLogs) == 0 || m.historyCursor >= len(m.historyLogs) {
		m.historyStatus, m.historyStatusErr = "没有可查看的启动日志", true
		return m, nil
	}
	selected := m.historyLogs[m.historyCursor]
	text, err := readLaunchLogTail(selected.Path)
	if err != nil {
		m.historyStatus, m.historyStatusErr = err.Error(), true
		return m, nil
	}
	m.logPath = selected.Path
	m.logText = normalizeLog(text)
	m.launchErr = nil
	m.launchCleanupErr = nil
	m.diagnostics = AnalyzeStoredLaunchLog(m.logText)
	m.historyLogFailed = len(m.diagnostics) > 0
	m.showAnalysis = len(m.diagnostics) > 0
	m.activeSession = nil
	m.showHistory = false
	m.showLog = true
	m.logView.SetContent(m.logDisplayContent())
	if m.showAnalysis {
		m.logView.GotoTop()
	} else {
		m.logView.GotoBottom()
	}
	return m, nil
}

func (m model) historyView() string {
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("启动历史 · "+activeInstanceDisplay(m.launcher)) + "\n\n")
	if len(m.historyLogs) == 0 {
		builder.WriteString(dimStyle.Render("当前实例还没有日志；启动一次游戏后会自动出现在这里。") + "\n")
	} else {
		start, end := visibleHistoryRange(m.historyCursor, len(m.historyLogs), max(4, m.height-8))
		for index := start; index < end; index++ {
			log := m.historyLogs[index]
			cursor := "  "
			style := dimStyle
			if index == m.historyCursor {
				cursor = "› "
				style = selectedStyle
			}
			line := fmt.Sprintf("%s%s  %s  %s", cursor, log.ModTime.Format("2006-01-02 15:04:05"), humanBytes(log.Size), log.Name)
			builder.WriteString(style.Render(clampText(line, max(30, m.width-2))) + "\n")
		}
	}
	if m.historyStatus != "" {
		style := okStyle
		if m.historyStatusErr {
			style = errStyle
		}
		builder.WriteString("\n" + style.Render(clampText(m.historyStatus, max(30, m.width-2))) + "\n")
	}
	builder.WriteString("\n" + dimStyle.Render("↑/↓ 选择  Enter 查看并诊断  D×2 删除  R 刷新  Esc 返回"))
	return builder.String()
}

func visibleHistoryRange(cursor, total, height int) (int, int) {
	if total <= height {
		return 0, total
	}
	start := cursor - height/2
	if start < 0 {
		start = 0
	}
	if start+height > total {
		start = total - height
	}
	return start, start + height
}

func historyMenuDisplay(m model) string {
	if m.launching {
		return "正在记录 " + logFileName(m.logPath)
	}
	if len(m.historyLogs) == 0 {
		return logFileName(m.logPath)
	}
	return fmt.Sprintf("%d 份 · 最近 %s", len(m.historyLogs), m.historyLogs[0].ModTime.Format("01-02 15:04"))
}
