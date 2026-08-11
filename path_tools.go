package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

func openPath(path string) error {
	if path == "" {
		return fmt.Errorf("路径为空")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("路径不可用 %s: %w", path, err)
	}
	cmd, err := openPathCommand(path)
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("打开路径 %s: %w", path, err)
	}
	return nil
}

func openPathCommand(path string) (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", path), nil
	case "darwin":
		return exec.Command("open", path), nil
	case "linux":
		if shouldLaunchOnFlatpakHost() {
			if spawn, err := exec.LookPath("flatpak-spawn"); err == nil {
				return exec.Command(spawn, "--host", "xdg-open", path), nil
			}
		}
		if opener, err := exec.LookPath("xdg-open"); err == nil {
			return exec.Command(opener, path), nil
		}
		return nil, fmt.Errorf("找不到 xdg-open")
	default:
		return nil, fmt.Errorf("暂不支持在 %s 打开文件管理器", runtime.GOOS)
	}
}
