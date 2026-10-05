package app

import (
	"errors"
	"fmt"
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

// LauncherConfig is the only on-disk configuration. Instances are a slice so
// their order is stable in the TUI and in source control.
type LauncherConfig struct {
	ActiveInstanceID string           `json:"active_instance_id"`
	Instances        []InstanceConfig `json:"instances"`

	// Warnings contains recoverable load issues. It is intentionally not saved.
	Warnings []string `json:"-"`
}

// InstanceConfig contains everything needed to launch one executable Java
// game. Java/JAR/working directory paths are relative to the config, while
// data_directory is relative to the JAR.
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
		ActiveInstanceID: defaultInstanceID,
		Instances: []InstanceConfig{
			newInstanceConfig(defaultInstanceID, defaultInstanceName, cfg),
		},
	}
}

// clone returns a detached snapshot for normalization and asynchronous work.
// A value copy alone would still share instances and their argument slices.
func (launcher LauncherConfig) clone() LauncherConfig {
	launcher.Instances = slices.Clone(launcher.Instances)
	for i := range launcher.Instances {
		launcher.Instances[i].JVMArgs = slices.Clone(launcher.Instances[i].JVMArgs)
		launcher.Instances[i].GameArgs = slices.Clone(launcher.Instances[i].GameArgs)
	}
	launcher.Warnings = slices.Clone(launcher.Warnings)
	return launcher
}

// Config returns an independent launch view. Argument slices are
// cloned so editing a view cannot mutate the stored instance accidentally.
func (instance InstanceConfig) Config() Config {
	return Config{
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
