//go:build !windows

package fsutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ReplaceFile atomically publishes oldPath at newPath and syncs its directory.
func ReplaceFile(oldPath, newPath string) error {
	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(newPath))
	if err != nil {
		return fmt.Errorf("打开配置目录以同步: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil &&
		!errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTSUP) {
		return fmt.Errorf("同步配置目录: %w", err)
	}
	return nil
}
