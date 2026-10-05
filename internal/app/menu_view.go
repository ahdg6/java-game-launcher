package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ahdg6/java-game-launcher/internal/java"
	"github.com/charmbracelet/lipgloss"
)

func (m model) View() string {
	if m.mode != editNone {
		return m.editorView()
	}
	if m.picking {
		return m.pickerView()
	}
	switch m.page {
	case pageLog:
		return m.logViewPage()
	case pageHistory:
		return m.historyView()
	case pageZulu:
		return m.zuluView()
	case pagePreflight:
		return m.preflightView()
	case pageBackups:
		return m.backupsView()
	case pageMods:
		return m.modsView()
	case pageTools:
		return m.toolsView()
	case pageInstances:
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
		{"Java 运行时", shortPath(m.cfg.JavaPath, m.width-28)},
		{"游戏 JAR", shortPath(m.cfg.JarPath, m.width-28)},
		{"游戏配置", profileDisplayForModel(m)},
		{"工作目录", displayDefault(m.cfg.WorkingDirectory, "自动：JAR 所在目录")},
		{"数据目录", dataDirectoryDisplay(m, m.width-28)},
		{"Mindustry 工具", "备份/恢复 · 模组 · 安全启动"},
		{"JVM 参数", fmt.Sprintf("%s · %d 项", jvmPresetDisplay(m.cfg, m.memory), len(m.cfg.JVMArgs))},
		{"游戏参数", fmt.Sprintf("%d 项", len(m.cfg.GameArgs))},
		{"启动前检查", "Java · 模块 · 参数 · 目录 · 图形会话"},
		{"启动历史", historyMenuDisplay(m)},
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

func jvmPresetDisplay(cfg Config, memory java.MemoryInfo) string {
	if cfg.JVMPreset == "" || cfg.JVMPreset == presetCustom {
		return "自定义"
	}
	return java.ResolveJVMPreset(cfg.JVMPreset, memory).Name
}
