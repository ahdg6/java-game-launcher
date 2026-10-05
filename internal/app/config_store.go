package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ahdg6/java-game-launcher/internal/fsutil"
	"github.com/ahdg6/java-game-launcher/internal/java"
)

func loadLauncherConfig(path string) (LauncherConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaultLauncherConfig(), nil
	}
	if err != nil {
		return LauncherConfig{}, fmt.Errorf("读取配置: %w", err)
	}

	var launcher LauncherConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&launcher); err != nil {
		return LauncherConfig{}, fmt.Errorf("解析配置 %s: %w；配置尚未发布，直接删除此文件可重建", path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("配置包含多个 JSON 值")
		}
		return LauncherConfig{}, fmt.Errorf("解析配置 %s: %w；配置尚未发布，直接删除此文件可重建", path, err)
	}
	if err := normalizeLauncherConfig(&launcher); err != nil {
		return LauncherConfig{}, fmt.Errorf("校验配置 %s: %w；配置尚未发布，直接删除此文件可重建", path, err)
	}
	return launcher, nil
}

func normalizeLauncherConfig(launcher *LauncherConfig) error {
	if len(launcher.Instances) == 0 {
		return errors.New("至少需要一个实例")
	}
	seen := make(map[string]struct{}, len(launcher.Instances))
	for i := range launcher.Instances {
		instance := &launcher.Instances[i]
		if !instanceIDPattern.MatchString(instance.ID) {
			return fmt.Errorf("第 %d 个实例 ID %q 非法；应匹配 %s", i+1, instance.ID, instanceIDPattern.String())
		}
		if _, exists := seen[instance.ID]; exists {
			return fmt.Errorf("实例 ID %q 重复", instance.ID)
		}
		seen[instance.ID] = struct{}{}
		if strings.TrimSpace(instance.Name) == "" {
			instance.Name = instance.ID
			launcher.Warnings = append(launcher.Warnings,
				fmt.Sprintf("实例 %q 的名称为空，已临时使用其 ID", instance.ID))
		}
		if instance.GameProfile != "" && !slices.Contains(configuredProfileIDs(), instance.GameProfile) {
			return fmt.Errorf("实例 %q 的游戏配置 %q 未知", instance.ID, instance.GameProfile)
		}
		if instance.JVMPreset != "" && !validJVMPresetID(instance.JVMPreset) {
			return fmt.Errorf("实例 %q 的 JVM 预设 %q 未知", instance.ID, instance.JVMPreset)
		}
		normalizeInstanceConfig(instance)
	}
	if _, exists := seen[launcher.ActiveInstanceID]; !exists {
		old := launcher.ActiveInstanceID
		launcher.ActiveInstanceID = launcher.Instances[0].ID
		launcher.Warnings = append(launcher.Warnings,
			fmt.Sprintf("活动实例 %q 不存在，已临时回退到 %q", old, launcher.ActiveInstanceID))
	}
	return nil
}

func validJVMPresetID(id string) bool {
	switch id {
	case java.PresetAuto, java.PresetConservative, java.PresetBalanced, java.PresetPerformance, presetCustom:
		return true
	default:
		return false
	}
}

func normalizeInstanceConfig(instance *InstanceConfig) {
	if instance.GameProfile == "" {
		instance.GameProfile = profileAuto
	}
	if instance.JVMPreset == "" {
		instance.JVMPreset = java.PresetAuto
	}
	// nil means the field was absent. An explicit [] is an intentional request
	// for no JVM arguments and must survive a round trip.
	if instance.JVMPreset == presetCustom {
		if instance.JVMArgs == nil {
			instance.JVMArgs = defaultConfig().JVMArgs
		}
	} else {
		preset := java.ResolveJVMPreset(instance.JVMPreset, java.DetectMemory())
		instance.JVMPreset = preset.ID
		if instance.JVMArgs == nil || len(instance.JVMArgs) > 0 {
			instance.JVMArgs = preset.Args
		}
	}
	instance.JVMArgs = removeManagedDataDirectoryArgs(instance.JVMArgs)
	if instance.GameArgs == nil {
		instance.GameArgs = []string{}
	}
}

func saveLauncherConfig(path string, launcher LauncherConfig) error {
	launcher = launcher.clone()
	launcher.Warnings = nil
	if err := normalizeLauncherConfig(&launcher); err != nil {
		return fmt.Errorf("校验配置: %w", err)
	}
	data, err := json.MarshalIndent(launcher, "", "  ")
	if err != nil {
		return fmt.Errorf("编码配置: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建配置目录: %w", err)
	}
	if err := atomicWriteConfig(path, data, 0o644); err != nil {
		return fmt.Errorf("保存配置: %w", err)
	}
	return nil
}

func atomicWriteConfig(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".launcher-config-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		_ = tmp.Close()
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := fsutil.ReplaceFile(tmpPath, path); err != nil {
		return err
	}
	removeTemp = false
	return nil
}
