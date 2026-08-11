package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const (
	defaultInstanceID   = "default"
	defaultInstanceName = "默认实例"
)

var instanceIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// LauncherConfig is the v6 on-disk configuration. Instances are a slice so
// their order is stable in the TUI and in source control.
type LauncherConfig struct {
	Version          int              `json:"version"`
	ActiveInstanceID string           `json:"active_instance_id"`
	Instances        []InstanceConfig `json:"instances"`

	// Warnings contains recoverable load issues. It is intentionally not saved.
	Warnings []string `json:"-"`
}

// InstanceConfig contains everything needed to launch one executable Java
// game. Paths retain their existing v5 bases: Java/JAR/working directory are
// relative to the config, while data_directory is relative to the JAR.
type InstanceConfig struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	GameProfile      string   `json:"game_profile"`
	JavaPath         string   `json:"java_path"`
	JarPath          string   `json:"jar_path"`
	WorkingDirectory string   `json:"working_directory,omitempty"`
	DataDirectory    string   `json:"data_directory"`
	JVMPreset        string   `json:"jvm_preset"`
	JVMArgs          []string `json:"jvm_args"`
	GameArgs         []string `json:"game_args"`
}

func defaultLauncherConfig() LauncherConfig {
	cfg := defaultConfig()
	return LauncherConfig{
		Version:          configVersion,
		ActiveInstanceID: defaultInstanceID,
		Instances: []InstanceConfig{
			newInstanceConfig(defaultInstanceID, defaultInstanceName, cfg),
		},
	}
}

// Config returns an independent compatibility view. Argument slices are
// cloned so editing a view cannot mutate the stored instance accidentally.
func (instance InstanceConfig) Config() Config {
	return Config{
		Version:          configVersion,
		InstanceID:       instance.ID,
		GameProfile:      instance.GameProfile,
		JavaPath:         instance.JavaPath,
		JarPath:          instance.JarPath,
		WorkingDirectory: instance.WorkingDirectory,
		DataDirectory:    instance.DataDirectory,
		JVMPreset:        instance.JVMPreset,
		JVMArgs:          slices.Clone(instance.JVMArgs),
		GameArgs:         slices.Clone(instance.GameArgs),
	}
}

// ApplyConfig updates launch fields while preserving the instance's stable ID
// and display name.
func (instance *InstanceConfig) ApplyConfig(cfg Config) {
	instance.GameProfile = cfg.GameProfile
	instance.JavaPath = cfg.JavaPath
	instance.JarPath = cfg.JarPath
	instance.WorkingDirectory = cfg.WorkingDirectory
	instance.DataDirectory = cfg.DataDirectory
	instance.JVMPreset = cfg.JVMPreset
	instance.JVMArgs = slices.Clone(cfg.JVMArgs)
	instance.GameArgs = slices.Clone(cfg.GameArgs)
}

func newInstanceConfig(id, name string, cfg Config) InstanceConfig {
	instance := InstanceConfig{ID: id, Name: name}
	instance.ApplyConfig(cfg)
	return instance
}

func loadLauncherConfig(path string) (LauncherConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaultLauncherConfig(), nil
	}
	if err != nil {
		return LauncherConfig{}, fmt.Errorf("读取配置: %w", err)
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return LauncherConfig{}, fmt.Errorf("解析配置 %s: %w", path, err)
	}
	var header struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return LauncherConfig{}, fmt.Errorf("解析配置 %s: %w", path, err)
	}
	_, hasInstances := root["instances"]
	if header.Version > configVersion {
		return LauncherConfig{}, fmt.Errorf("配置版本 %d 比启动器支持的版本 %d 更新", header.Version, configVersion)
	}
	if hasInstances && header.Version != configVersion {
		return LauncherConfig{}, fmt.Errorf("配置包含 instances，但版本为 %d；拒绝按旧版单实例格式覆盖", header.Version)
	}
	if header.Version < configVersion || !hasInstances {
		legacy, err := decodeLegacyConfig(data)
		if err != nil {
			return LauncherConfig{}, fmt.Errorf("解析旧配置 %s: %w", path, err)
		}
		return LauncherConfig{
			Version:          configVersion,
			ActiveInstanceID: defaultInstanceID,
			Instances: []InstanceConfig{
				newInstanceConfig(defaultInstanceID, defaultInstanceName, legacy),
			},
		}, nil
	}

	var launcher LauncherConfig
	if err := json.Unmarshal(data, &launcher); err != nil {
		return LauncherConfig{}, fmt.Errorf("解析配置 %s: %w", path, err)
	}
	if err := normalizeLauncherConfig(&launcher); err != nil {
		return LauncherConfig{}, fmt.Errorf("校验配置 %s: %w", path, err)
	}
	return launcher, nil
}

func normalizeLauncherConfig(launcher *LauncherConfig) error {
	launcher.Version = configVersion
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

func normalizeInstanceConfig(instance *InstanceConfig) {
	if instance.GameProfile == "" {
		instance.GameProfile = profileAuto
	}
	if instance.JVMPreset == "" {
		instance.JVMPreset = presetAuto
	}
	// nil means the field was absent. An explicit [] is an intentional request
	// for no JVM arguments and must survive a v6 round trip.
	if instance.JVMPreset == presetCustom {
		if instance.JVMArgs == nil {
			instance.JVMArgs = defaultConfig().JVMArgs
		}
	} else {
		preset := ResolveJVMPreset(instance.JVMPreset, DetectMemory())
		instance.JVMPreset = preset.ID
		if instance.JVMArgs == nil || len(instance.JVMArgs) > 0 {
			instance.JVMArgs = preset.Args
		}
	}
	instance.JVMArgs, instance.DataDirectory = normalizeDataDirectoryArg(
		instance.JVMArgs, instance.DataDirectory, configVersion,
	)
	if instance.GameArgs == nil {
		instance.GameArgs = []string{}
	}
}

func saveLauncherConfig(path string, launcher LauncherConfig) error {
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
	if err := backupLegacyConfig(path); err != nil {
		return err
	}
	if err := atomicWriteConfig(path, data, 0o644); err != nil {
		return fmt.Errorf("保存配置: %w", err)
	}
	return nil
}

func backupLegacyConfig(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取待迁移配置: %w", err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		// The caller already loaded/validated the config in normal flows. Avoid
		// claiming malformed data is a migratable v5 document.
		return nil
	}
	var version int
	if raw := root["version"]; raw != nil {
		_ = json.Unmarshal(raw, &version)
	}
	_, hasInstances := root["instances"]
	if version >= configVersion && hasInstances {
		return nil
	}
	backupPath := path + ".v5.bak"
	if _, err := os.Stat(backupPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查迁移备份: %w", err)
	}
	if err := atomicWriteConfig(backupPath, data, 0o644); err != nil {
		return fmt.Errorf("保存迁移备份 %s: %w", backupPath, err)
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
	if err := replaceFileAtomic(tmpPath, path); err != nil {
		return err
	}
	removeTemp = false
	return nil
}

func (launcher *LauncherConfig) Active() (*InstanceConfig, error) {
	instance := launcher.InstanceByID(launcher.ActiveInstanceID)
	if instance == nil {
		return nil, fmt.Errorf("活动实例 %q 不存在", launcher.ActiveInstanceID)
	}
	return instance, nil
}

func (launcher *LauncherConfig) InstanceByID(id string) *InstanceConfig {
	for i := range launcher.Instances {
		if launcher.Instances[i].ID == id {
			return &launcher.Instances[i]
		}
	}
	return nil
}

// ResolveInstance selects by stable ID first. A display name is accepted only
// when it identifies exactly one instance.
func (launcher *LauncherConfig) ResolveInstance(selector string) (*InstanceConfig, error) {
	if instance := launcher.InstanceByID(selector); instance != nil {
		return instance, nil
	}
	var match *InstanceConfig
	for i := range launcher.Instances {
		if launcher.Instances[i].Name != selector {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("实例名称 %q 不唯一，请改用实例 ID", selector)
		}
		match = &launcher.Instances[i]
	}
	if match == nil {
		return nil, fmt.Errorf("找不到实例 %q", selector)
	}
	return match, nil
}

func (launcher *LauncherConfig) SelectInstance(selector string) (*InstanceConfig, error) {
	instance, err := launcher.ResolveInstance(selector)
	if err != nil {
		return nil, err
	}
	launcher.ActiveInstanceID = instance.ID
	return instance, nil
}

func (launcher *LauncherConfig) CreateInstance(id, name string) (*InstanceConfig, error) {
	if !instanceIDPattern.MatchString(id) {
		return nil, fmt.Errorf("实例 ID %q 非法；应匹配 %s", id, instanceIDPattern.String())
	}
	if launcher.InstanceByID(id) != nil {
		return nil, fmt.Errorf("实例 ID %q 已存在", id)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = id
	}
	cfg := defaultConfig()
	cfg.DataDirectory = filepath.Join("instances", id, "game_data")
	launcher.Instances = append(launcher.Instances, newInstanceConfig(id, name, cfg))
	return &launcher.Instances[len(launcher.Instances)-1], nil
}

func (launcher *LauncherConfig) CloneInstance(sourceSelector, id, name string) (*InstanceConfig, error) {
	source, err := launcher.ResolveInstance(sourceSelector)
	if err != nil {
		return nil, err
	}
	if !instanceIDPattern.MatchString(id) {
		return nil, fmt.Errorf("实例 ID %q 非法；应匹配 %s", id, instanceIDPattern.String())
	}
	if launcher.InstanceByID(id) != nil {
		return nil, fmt.Errorf("实例 ID %q 已存在", id)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = source.Name + " 副本"
	}
	clone := *source
	clone.ID = id
	clone.Name = name
	clone.DataDirectory = filepath.Join("instances", id, "game_data")
	clone.JVMArgs = slices.Clone(source.JVMArgs)
	clone.GameArgs = slices.Clone(source.GameArgs)
	launcher.Instances = append(launcher.Instances, clone)
	return &launcher.Instances[len(launcher.Instances)-1], nil
}

func (launcher *LauncherConfig) RenameInstance(selector, name string) error {
	instance, err := launcher.ResolveInstance(selector)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("实例名称不能为空")
	}
	instance.Name = name
	return nil
}

func (launcher *LauncherConfig) DeleteInstance(selector string) error {
	if len(launcher.Instances) <= 1 {
		return errors.New("不能删除最后一个实例")
	}
	instance, err := launcher.ResolveInstance(selector)
	if err != nil {
		return err
	}
	deletedID := instance.ID
	for i := range launcher.Instances {
		if launcher.Instances[i].ID != deletedID {
			continue
		}
		launcher.Instances = append(launcher.Instances[:i], launcher.Instances[i+1:]...)
		break
	}
	if launcher.ActiveInstanceID == deletedID {
		launcher.ActiveInstanceID = launcher.Instances[0].ID
	}
	return nil
}

// MoveInstance moves an instance by offset positions and clamps at either end.
func (launcher *LauncherConfig) MoveInstance(selector string, offset int) error {
	instance, err := launcher.ResolveInstance(selector)
	if err != nil {
		return err
	}
	from := slices.IndexFunc(launcher.Instances, func(candidate InstanceConfig) bool {
		return candidate.ID == instance.ID
	})
	to := min(max(from+offset, 0), len(launcher.Instances)-1)
	if from == to {
		return nil
	}
	moved := launcher.Instances[from]
	if from < to {
		copy(launcher.Instances[from:to], launcher.Instances[from+1:to+1])
	} else {
		copy(launcher.Instances[to+1:from+1], launcher.Instances[to:from])
	}
	launcher.Instances[to] = moved
	return nil
}
