package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type backupRestoreMsg struct {
	restored RestoreResult
	safety   BackupResult
	err      error
}

func (m model) updateBackups(msg tea.Msg) (tea.Model, tea.Cmd) {
	if result, ok := msg.(backupRestoreMsg); ok {
		m.backupBusy = false
		m.confirmRestoreBackup = false
		if result.err != nil {
			m.backupStatus = result.err.Error()
			if result.safety.Path != "" {
				m.lastBackup = result.safety
				m.backupStatus += "；当前数据保护备份已保留在 " + result.safety.Path
				m.refreshBackupCount()
			}
			m.backupStatusErr = true
			return m, nil
		}
		safetyLabel := result.safety.Path
		if safetyLabel == "" {
			safetyLabel = "目标原先不存在，无需创建"
		}
		completionStatus := fmt.Sprintf(
			"恢复完成：写入 %d 个文件（%s）；恢复前保护备份：%s",
			result.restored.FileCount,
			humanBytes(result.restored.BytesWritten),
			safetyLabel,
		)
		m.lastBackup = result.safety
		m.refreshBackups()
		m.refreshBackupCount()
		m.backupStatus = completionStatus
		m.backupStatusErr = false
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if m.backupBusy {
		if key.String() == "ctrl+c" {
			m.backupStatus, m.backupStatusErr = "恢复正在进行，完成前不能退出启动器", true
		}
		return m, nil
	}
	switch key.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.showBackups = false
		m.confirmRestoreBackup = false
		return m, nil
	case "up", "k":
		if len(m.backups) > 0 {
			m.backupsCursor = (m.backupsCursor - 1 + len(m.backups)) % len(m.backups)
			m.refreshBackupPreview()
		}
		m.confirmRestoreBackup = false
	case "down", "j":
		if len(m.backups) > 0 {
			m.backupsCursor = (m.backupsCursor + 1) % len(m.backups)
			m.refreshBackupPreview()
		}
		m.confirmRestoreBackup = false
	case "enter", " ":
		m.refreshBackupPreview()
		m.confirmRestoreBackup = false
	case "r":
		return m.restoreSelectedBackup()
	case "f":
		m.refreshBackups()
		m.confirmRestoreBackup = false
	}
	return m, nil
}

func (m *model) refreshBackups() {
	backups, err := ListBackups(m.backupDirectory())
	if err != nil {
		m.backups = nil
		m.backupPreview = BackupPreview{}
		m.backupStatus, m.backupStatusErr = err.Error(), true
		return
	}
	m.backups = backups
	if len(backups) == 0 {
		m.backupsCursor = 0
		m.backupPreview = BackupPreview{}
		m.backupStatus, m.backupStatusErr = "当前实例还没有备份", false
		return
	}
	if m.backupsCursor >= len(backups) {
		m.backupsCursor = len(backups) - 1
	}
	m.refreshBackupPreview()
}

func (m *model) refreshBackupPreview() {
	if len(m.backups) == 0 || m.backupsCursor < 0 || m.backupsCursor >= len(m.backups) {
		m.backupPreview = BackupPreview{}
		return
	}
	preview, err := InspectDataBackup(m.backups[m.backupsCursor].Path)
	if err != nil {
		m.backupPreview = BackupPreview{}
		m.backupStatus, m.backupStatusErr = "备份校验失败："+err.Error(), true
		return
	}
	m.backupPreview = preview
	m.backupStatus = fmt.Sprintf("备份安全校验通过：%d 个文件，解压后 %s", preview.FileCount, humanBytesUint64(preview.UncompressedBytes))
	m.backupStatusErr = false
}

func (m model) restoreSelectedBackup() (tea.Model, tea.Cmd) {
	if m.launching {
		m.backupStatus, m.backupStatusErr = "游戏或服务器运行时不能恢复数据", true
		return m, nil
	}
	if len(m.backups) == 0 || m.backupsCursor >= len(m.backups) {
		m.backupStatus, m.backupStatusErr = "没有可恢复的备份", true
		return m, nil
	}
	dataDir := resolveDataDirectory(m.cfg, m.cfgPath)
	if dataDir == "" {
		m.backupStatus, m.backupStatusErr = "请先为当前实例配置明确的数据目录", true
		return m, nil
	}
	if m.backupPreview.Path != m.backups[m.backupsCursor].Path {
		m.refreshBackupPreview()
		if m.backupStatusErr {
			return m, nil
		}
	}
	selected := m.backups[m.backupsCursor]
	if !m.confirmRestoreBackup {
		m.confirmRestoreBackup = true
		m.backupStatus = "再次按 R 确认合并恢复 “" + selected.Name + "”；现有同名文件会被覆盖，恢复前会先自动备份当前数据"
		m.backupStatusErr = true
		return m, nil
	}
	m.backupBusy = true
	m.backupStatus, m.backupStatusErr = "正在保护当前数据并恢复备份，请勿关闭启动器…", false
	backupDir := m.backupDirectory()
	return m, func() tea.Msg {
		var safety BackupResult
		info, statErr := os.Stat(dataDir)
		switch {
		case statErr == nil && info.IsDir():
			var err error
			safety, err = CreateDataBackup(dataDir, backupDir)
			if err != nil {
				return backupRestoreMsg{err: fmt.Errorf("恢复前保护当前数据失败，已取消恢复：%w", err)}
			}
		case statErr == nil:
			return backupRestoreMsg{err: fmt.Errorf("恢复目标不是目录：%s", dataDir)}
		case !errors.Is(statErr, os.ErrNotExist):
			return backupRestoreMsg{err: fmt.Errorf("检查恢复目标：%w", statErr)}
		}
		restored, err := RestoreDataBackup(selected.Path, dataDir, RestoreOptions{Overwrite: true})
		if err != nil {
			return backupRestoreMsg{safety: safety, err: err}
		}
		return backupRestoreMsg{restored: restored, safety: safety}
	}
}

func (m model) backupsView() string {
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("Mindustry 数据备份"))
	if m.backupBusy {
		builder.WriteString(selectedStyle.Render("  [恢复中]"))
	}
	builder.WriteString("\n\n")
	if len(m.backups) == 0 {
		builder.WriteString(dimStyle.Render("没有备份；返回工具页可立即创建。") + "\n")
	} else {
		start, end := visibleBackupRange(m.backupsCursor, len(m.backups), max(3, m.height-13))
		for index := start; index < end; index++ {
			backup := m.backups[index]
			cursor := "  "
			style := dimStyle
			if index == m.backupsCursor {
				cursor = "› "
				style = selectedStyle
			}
			line := fmt.Sprintf("%s%s  %s  %s", cursor, backup.Name, humanBytes(backup.Size), backup.ModTime.Format("2006-01-02 15:04:05"))
			builder.WriteString(style.Render(clampText(line, max(30, m.width-2))) + "\n")
		}
	}
	if m.backupPreview.Path != "" {
		contents := strings.Join(m.backupPreview.TopLevel, " · ")
		if contents == "" {
			contents = "空备份"
		}
		builder.WriteString("\n" + labelStyle.Render("预览  ") + fmt.Sprintf(
			"%d 文件 · %d 目录 · %s\n",
			m.backupPreview.FileCount,
			m.backupPreview.DirectoryCount,
			humanBytesUint64(m.backupPreview.UncompressedBytes),
		))
		builder.WriteString(labelStyle.Render("内容  ") + clampText(contents, max(20, m.width-10)) + "\n")
	}
	if m.backupStatus != "" {
		style := okStyle
		if m.backupStatusErr {
			style = errStyle
		}
		builder.WriteString("\n" + style.Render(clampText(m.backupStatus, max(30, m.width-2))) + "\n")
	}
	builder.WriteString("\n" + dimStyle.Render("↑/↓ 选择  Enter 重新校验  R×2 合并恢复  F 刷新  Esc 返回"))
	return builder.String()
}

func visibleBackupRange(cursor, total, height int) (int, int) {
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

func humanBytesUint64(size uint64) string {
	const maxInt64 = uint64(^uint64(0) >> 1)
	if size > maxInt64 {
		return "> 8 EiB"
	}
	return humanBytes(int64(size))
}
