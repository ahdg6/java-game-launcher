package app

import (
	"fmt"
	"strings"

	"github.com/ahdg6/java-game-launcher/internal/diagnostics"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type logState struct {
	serverInput       textinput.Model
	logView           viewport.Model
	logText           string
	logPath           string
	launchErr         error
	historyLogFailed  bool
	launchCleanupErr  error
	diagnostics       []diagnostics.Diagnostic
	showAnalysis      bool
	consoleInput      bool
	serverStopPending bool
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
						m.appendLiveLog("[控制台] > " + command + "\n")
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
			m.closePage(pageLog, pageMain)
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
						m.appendLiveLog("\n[启动器] 已发送 exit，请等待服务器保存并退出；再次按 Ctrl+X 可强制终止。\n")
					}
				} else if err := m.activeSession.Stop(); err != nil {
					m.setStatus("强制停止服务器失败："+err.Error(), true)
				} else {
					m.appendLiveLog("\n[启动器] 已强制终止服务器进程。\n")
				}
				return m, nil
			}
		case "m":
			if m.canRetryWithoutMods() {
				return m.startSafeMode(resolveDataDirectory(m.cfg, m.cfgPath))
			}
		}
	}
	var cmd tea.Cmd
	m.logView, cmd = m.logView.Update(msg)
	return m, cmd
}

func (m *model) appendLog(text string) {
	follow := m.logView.AtBottom()
	m.logText += normalizeLog(text)
	if len(m.logText) > maxCapturedLogBytes {
		tail := strings.ToValidUTF8(m.logText[len(m.logText)-maxCapturedLogBytes:], "")
		m.logText = "[启动器] TUI 仅保留最后 4 MiB；完整内容请查看日志文件。\n\n" +
			tail
	}
	m.logView.SetContent(m.logDisplayContent())
	if follow {
		m.logView.GotoBottom()
	}
}

func (m *model) appendLiveLog(text string) {
	if m.launching && m.activeSession != nil {
		_, _ = m.activeSession.writer.Write([]byte(text))
		m.replaceLiveLog(m.activeSession.writer.output())
		return
	}
	m.appendLog(text)
}

func (m *model) replaceLiveLog(raw string) {
	follow := m.logView.AtBottom()
	m.logText = normalizeLog(raw)
	m.logView.SetContent(m.logDisplayContent())
	if follow {
		m.logView.GotoBottom()
	}
}

func (m model) canRetryWithoutMods() bool {
	if m.loading || m.preparing != nil || m.launching || m.launchErr == nil || m.activeSession == nil || strings.TrimSpace(m.cfg.DataDirectory) == "" {
		return false
	}
	spec := m.activeSession.spec
	adapter := effectiveAdapter(m.cfg, spec.Jar)
	return adapter.ID() == profileMindustry && spec.NeedsGraphics && !spec.InteractiveConsole
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
	if m.preparing != nil {
		state = selectedStyle.Render("正在校验 Java、JAR 和启动配置，请稍候…")
	} else if m.launching {
		if m.activeSession != nil && m.activeSession.spec.InteractiveConsole {
			state = selectedStyle.Render("服务器正在运行，日志实时更新中…")
		} else {
			state = selectedStyle.Render("游戏正在运行，日志实时更新中…")
		}
	} else if m.launchCleanupErr != nil {
		state = errStyle.Render("退出后恢复失败：" + m.launchCleanupErr.Error())
	} else if m.launchErr != nil {
		state = errStyle.Render("启动失败：" + m.launchErr.Error())
	} else if m.historyLogFailed {
		state = errStyle.Render("历史启动失败")
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
	if m.canRetryWithoutMods() {
		footer += "  M 仅本次无模组重试"
	}
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

func (m *model) loadLatestInstanceLog() {
	logs, err := listLaunchLogs(m.cfgPath, m.cfg.InstanceID)
	if err != nil {
		if m.status == "" {
			m.setStatus(err.Error(), true)
		}
		return
	}
	m.historyLogs = logs
	if len(logs) == 0 {
		return
	}
	output, err := readLaunchLogTail(logs[0].Path)
	if err != nil {
		if m.status == "" {
			m.setStatus(err.Error(), true)
		}
		return
	}
	m.logPath = logs[0].Path
	m.logText = normalizeLog(output)
	m.diagnostics = diagnostics.AnalyzeStoredLaunchLog(m.logText)
	m.historyLogFailed = len(m.diagnostics) > 0
	m.showAnalysis = m.historyLogFailed
	m.logView.SetContent(m.logDisplayContent())
	if m.showAnalysis {
		m.logView.GotoTop()
	} else {
		m.logView.GotoBottom()
	}
}
