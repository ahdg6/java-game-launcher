package main

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m model) updateMods(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.showMods = false
		m.confirmDisableAll = false
	case "up", "k":
		if len(m.mods) > 0 {
			m.modsCursor = (m.modsCursor - 1 + len(m.mods)) % len(m.mods)
		}
		m.confirmDisableAll = false
	case "down", "j":
		if len(m.mods) > 0 {
			m.modsCursor = (m.modsCursor + 1) % len(m.mods)
		}
		m.confirmDisableAll = false
	case "enter", " ":
		m.confirmDisableAll = false
		m.toggleSelectedMod()
	case "r":
		m.confirmDisableAll = false
		m.refreshMods()
	case "x":
		if m.launching {
			m.modsStatus, m.modsStatusErr = "游戏运行中不能修改模组状态", true
			return m, nil
		}
		if !m.confirmDisableAll {
			m.confirmDisableAll = true
			m.modsStatus, m.modsStatusErr = "再次按 X 确认禁用全部已启用模组；可在本页按 U 恢复", true
			return m, nil
		}
		plan, err := DisableAllMindustryMods(m.mods)
		if err != nil {
			m.modsStatus, m.modsStatusErr = err.Error(), true
		} else {
			m.modDisablePlan = plan
			m.modsStatus = fmt.Sprintf("已禁用 %d 个模组；按 U 可恢复本次操作", len(plan.Changes))
			m.modsStatusErr = false
			m.refreshMods()
		}
		m.confirmDisableAll = false
	case "u":
		m.confirmDisableAll = false
		if len(m.modDisablePlan.Changes) == 0 {
			m.modsStatus, m.modsStatusErr = "当前会话没有可恢复的批量禁用操作", true
			return m, nil
		}
		if err := RestoreMindustryMods(m.modDisablePlan); err != nil {
			m.modsStatus, m.modsStatusErr = err.Error(), true
		} else {
			count := len(m.modDisablePlan.Changes)
			m.modDisablePlan = MindustryModDisablePlan{}
			m.modsStatus = fmt.Sprintf("已恢复 %d 个模组", count)
			m.modsStatusErr = false
			m.refreshMods()
		}
	}
	return m, nil
}

func (m *model) refreshMods() {
	dataDir := resolveDataDirectory(m.cfg, m.cfgPath)
	mods, err := ScanMindustryMods(dataDir)
	if err != nil {
		m.modsStatus, m.modsStatusErr = err.Error(), true
		return
	}
	m.mods = mods
	if len(mods) == 0 {
		m.modsCursor = 0
		m.modsStatus, m.modsStatusErr = "没有发现模组", false
	} else if m.modsCursor >= len(mods) {
		m.modsCursor = len(mods) - 1
	}
}

func (m *model) toggleSelectedMod() {
	if m.launching {
		m.modsStatus, m.modsStatusErr = "游戏运行中不能修改模组状态", true
		return
	}
	if len(m.mods) == 0 || m.modsCursor >= len(m.mods) {
		m.modsStatus, m.modsStatusErr = "没有可切换的模组", true
		return
	}
	mod := m.mods[m.modsCursor]
	updated, err := SetMindustryModEnabled(mod, !mod.Enabled)
	if err != nil {
		m.modsStatus, m.modsStatusErr = err.Error(), true
		return
	}
	m.mods[m.modsCursor] = updated
	state := "启用"
	if !updated.Enabled {
		state = "禁用"
	}
	m.modsStatus = fmt.Sprintf("已%s %s", state, updated.DisplayName)
	m.modsStatusErr = false
}

func (m model) modsView() string {
	var builder strings.Builder
	enabled := 0
	for _, mod := range m.mods {
		if mod.Enabled {
			enabled++
		}
	}
	builder.WriteString(titleStyle.Render("Mindustry 模组管理"))
	builder.WriteString(fmt.Sprintf("  %d 个模组 · %d 启用 · %d 禁用\n\n", len(m.mods), enabled, len(m.mods)-enabled))
	if len(m.mods) == 0 {
		builder.WriteString(dimStyle.Render("没有发现模组；可返回工具页打开 mods 目录导入。") + "\n")
	} else {
		start, end := visibleModRange(m.modsCursor, len(m.mods), max(4, m.height-10))
		for index := start; index < end; index++ {
			mod := m.mods[index]
			cursor := "  "
			style := dimStyle
			if index == m.modsCursor {
				cursor = "› "
				style = selectedStyle
			}
			state := "○ 禁用"
			if mod.Enabled {
				state = "● 启用"
			}
			detail := mod.Version
			if mod.Author != "" {
				if detail != "" {
					detail += " · "
				}
				detail += mod.Author
			}
			if detail != "" {
				detail = "  " + detail
			}
			line := fmt.Sprintf("%s%s  %s%s  %s", cursor, state, mod.DisplayName, detail, humanBytes(mod.Size))
			builder.WriteString(style.Render(clampText(line, max(30, m.width-2))) + "\n")
			if index == m.modsCursor {
				builder.WriteString(dimStyle.Render("    "+filepath.Base(mod.Path)+" · "+string(mod.Type)) + "\n")
			}
		}
	}
	if m.modsStatus != "" {
		style := okStyle
		if m.modsStatusErr {
			style = errStyle
		}
		builder.WriteString("\n" + style.Render(clampText(m.modsStatus, max(30, m.width-2))) + "\n")
	}
	builder.WriteString("\n" + dimStyle.Render("↑/↓ 选择  Space/Enter 启停  R 重扫  X×2 全部禁用  U 恢复批量操作  Esc 返回"))
	return builder.String()
}

func visibleModRange(cursor, total, height int) (int, int) {
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
