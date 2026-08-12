package main

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type zuluMetadataMsg struct {
	pkg ZuluPackage
	err error
}

type zuluInstallMsg struct {
	result ZuluInstallResult
	err    error
}

func (m model) updateZulu(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch message := msg.(type) {
	case zuluMetadataMsg:
		m.zuluBusy = false
		if message.err != nil {
			m.zuluPackage = ZuluPackage{}
			m.zuluStatus, m.zuluStatusErr = message.err.Error(), true
			return m, nil
		}
		m.zuluPackage = message.pkg
		m.zuluStatus = "只查询了官方元数据，尚未下载；按 I 两次确认安装"
		m.zuluStatusErr = false
		m.confirmZuluInstall = false
		return m, nil
	case zuluInstallMsg:
		m.zuluBusy = false
		m.confirmZuluInstall = false
		if message.err != nil {
			m.zuluStatus, m.zuluStatusErr = message.err.Error(), true
			return m, nil
		}
		m.cfg.JavaPath = portablePath(m.cfgPath, message.result.JavaPath)
		m.dirty = true
		m.zuluStatus = "已校验并安装；当前实例已选用 " + m.cfg.JavaPath
		m.zuluStatusErr = false
		m.loading = true
		m.discoveryGeneration++
		return m, discoverCmd(m.cfg, m.cfgPath, m.discoveryGeneration)
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if m.zuluBusy {
		if key.String() == "ctrl+c" {
			m.zuluStatus, m.zuluStatusErr = "网络或安装操作进行中，完成前不能退出", true
		}
		return m, nil
	}
	switch key.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.showZulu = false
		m.confirmZuluInstall = false
		return m, nil
	case "p":
		m.showZulu = false
		return m, m.beginPathPicker(editJavaPath, "选择 Java 可执行文件")
	case "r":
		m.zuluBusy = true
		m.zuluPackage = ZuluPackage{}
		m.confirmZuluInstall = false
		m.zuluStatus, m.zuluStatusErr = "正在查询 Azul 官方元数据；不会下载文件…", false
		return m, fetchZuluMetadataCmd()
	case "i":
		if m.zuluPackage.UUID == "" {
			m.zuluStatus, m.zuluStatusErr = "请先按 R 查询官方最新 LTS 包", true
			return m, nil
		}
		if !m.confirmZuluInstall {
			m.confirmZuluInstall = true
			m.zuluStatus = "再次按 I 确认下载约 " + humanBytes(m.zuluPackage.Size) + " 并安装；不会替换或删除已有 Java"
			m.zuluStatusErr = true
			return m, nil
		}
		m.zuluBusy = true
		m.zuluStatus, m.zuluStatusErr = "正在从 cdn.azul.com 下载、校验 SHA-256 并安全解压…", false
		pkg := m.zuluPackage
		root := filepath.Join(configDir(m.cfgPath), "runtimes")
		return m, installZuluCmd(pkg, root)
	}
	return m, nil
}

func fetchZuluMetadataCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pkg, err := FetchLatestZuluLTS(ctx, &http.Client{Timeout: 30 * time.Second})
		return zuluMetadataMsg{pkg: pkg, err: err}
	}
}

func installZuluCmd(pkg ZuluPackage, root string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		result, err := InstallZuluPackage(ctx, &http.Client{Timeout: 20 * time.Minute}, pkg, root)
		return zuluInstallMsg{result: result, err: err}
	}
}

func (m model) zuluView() string {
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("Java 运行时 · Azul Zulu"))
	if m.zuluBusy {
		builder.WriteString(selectedStyle.Render("  [处理中]"))
	}
	builder.WriteString("\n\n")
	builder.WriteString(labelStyle.Render("当前实例  ") + displayDefault(m.cfg.JavaPath, "自动发现") + "\n")
	builder.WriteString(labelStyle.Render("安装位置  ") + filepath.Join(configDir(m.cfgPath), "runtimes") + "\n\n")
	if m.zuluPackage.UUID == "" {
		builder.WriteString(dimStyle.Render("没有联网查询。按 R 才会访问 api.azul.com，仅获取当前平台最新 LTS JRE 元数据。") + "\n")
	} else {
		builder.WriteString(labelStyle.Render("候选版本  ") + "Zulu JRE " + zuluVersionLabel(m.zuluPackage.JavaVersion) + " LTS\n")
		builder.WriteString(labelStyle.Render("官方包    ") + m.zuluPackage.Name + "\n")
		builder.WriteString(labelStyle.Render("大小      ") + humanBytes(m.zuluPackage.Size) + "\n")
		builder.WriteString(labelStyle.Render("SHA-256   ") + clampText(m.zuluPackage.SHA256, max(24, m.width-14)) + "\n")
		builder.WriteString(labelStyle.Render("来源      ") + "api.azul.com 元数据 + cdn.azul.com 下载\n")
	}
	if m.zuluStatus != "" {
		style := okStyle
		if m.zuluStatusErr {
			style = errStyle
		}
		builder.WriteString("\n" + style.Render(clampText(m.zuluStatus, max(30, m.width-2))) + "\n")
	}
	builder.WriteString("\n" + dimStyle.Render("R 只查询  I×2 下载并安装  P 选择本地 Java  Esc 返回"+zuluBusyHelp(m.zuluBusy)))
	return builder.String()
}

func zuluBusyHelp(busy bool) string {
	if busy {
		return "  · 当前操作完成前按键无效"
	}
	return ""
}
