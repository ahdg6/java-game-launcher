package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type preflightResultMsg struct{ report PreflightReport }

func (m model) startPreflight() (tea.Model, tea.Cmd) {
	if m.loading {
		m.setStatus("仍在检测 Java，请稍候", true)
		return m, nil
	}
	if m.launching {
		m.setStatus("游戏或服务器已经在运行", true)
		return m, nil
	}
	m.syncActiveInstance()
	m.showPreflight = true
	m.preflightBusy = true
	m.preflight = PreflightReport{}
	cfg := m.cfg
	configPath := m.cfgPath
	return m, func() tea.Msg {
		return preflightResultMsg{report: RunLaunchPreflight(cfg, configPath)}
	}
}

func (m model) updatePreflight(msg tea.Msg) (tea.Model, tea.Cmd) {
	if result, ok := msg.(preflightResultMsg); ok {
		m.preflightBusy = false
		m.preflight = result.report
		if result.report.Ready {
			m.setStatus("启动前检查通过，可以启动", false)
		} else {
			m.setStatus("启动前检查发现阻止启动的问题", true)
		}
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if m.preflightBusy {
		if key.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}
	switch key.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.showPreflight = false
		return m, nil
	case "r":
		return m.startPreflight()
	case "enter", " ":
		if !m.preflight.Ready {
			m.setStatus("请先解决检查页中的错误", true)
			return m, nil
		}
		return m.startConfiguredGame()
	}
	return m, nil
}

func (m model) preflightView() string {
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("启动前完整检查"))
	if m.preflightBusy {
		builder.WriteString(selectedStyle.Render("  [检查中]"))
	}
	builder.WriteString("\n\n")
	if m.preflightBusy {
		builder.WriteString(dimStyle.Render("正在验证 Java、游戏 JAR、模块、JVM 参数和目录权限…") + "\n")
	} else {
		for _, check := range m.preflight.Checks {
			icon := "✓"
			style := okStyle
			switch check.Level {
			case PreflightWarning:
				icon = "!"
				style = selectedStyle
			case PreflightError:
				icon = "✗"
				style = errStyle
			}
			builder.WriteString(style.Render(fmt.Sprintf("%s %s", icon, check.Name)) + "\n")
			builder.WriteString(dimStyle.Render("  "+clampText(check.Summary, max(24, m.width-4))) + "\n")
		}
		if m.preflight.Ready {
			builder.WriteString("\n" + okStyle.Render("检查通过：Enter 启动当前实例") + "\n")
		} else {
			builder.WriteString("\n" + errStyle.Render("存在阻止启动的错误；修正配置后按 R 重试") + "\n")
		}
		if m.preflight.Spec.Command != nil {
			builder.WriteString(dimStyle.Render("命令  "+clampText(formatCommand(m.preflight.Spec), max(24, m.width-4))) + "\n")
		}
	}
	builder.WriteString("\n" + dimStyle.Render("R 重新检查  Enter 启动（通过后）  Esc 返回"))
	return builder.String()
}
