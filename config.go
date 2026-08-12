package main

import (
	"os"
	"path/filepath"
	"strings"
)

const presetCustom = "custom"

const configFileName = "java-game-launcher.json"

type Config struct {
	InstanceID       string   `json:"-"`
	GameProfile      string   `json:"game_profile"`
	JavaPath         string   `json:"java_path"`
	JarPath          string   `json:"jar_path"`
	WorkingDirectory string   `json:"working_directory,omitempty"`
	DataDirectory    string   `json:"data_directory"`
	JVMPreset        string   `json:"jvm_preset"`
	JVMArgs          []string `json:"jvm_args"`
	GameArgs         []string `json:"game_args"`
}

func defaultConfig() Config {
	return Config{
		InstanceID:    defaultInstanceID,
		GameProfile:   profileAuto,
		DataDirectory: "game_data",
		JVMPreset:     presetAuto,
		JVMArgs:       ResolveJVMPreset(presetAuto, DetectMemory()).Args,
		GameArgs:      []string{},
	}
}

func removeManagedDataDirectoryArgs(args []string) []string {
	const prefix = "-Dmindustry.data.dir="
	clean := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			continue
		}
		clean = append(clean, arg)
	}
	return clean
}

func configDir(configPath string) string {
	dir := filepath.Dir(configPath)
	abs, err := filepath.Abs(dir)
	if err == nil {
		return abs
	}
	return dir
}

func resolveConfigPath(configPath, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(configDir(configPath), value))
}

// portablePath keeps files inside the launcher directory relative, so the whole
// folder can be moved or copied to another computer without editing the config.
func portablePath(configPath, value string) string {
	if value == "" {
		return ""
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return filepath.Clean(value)
	}
	rel, err := filepath.Rel(configDir(configPath), abs)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel
	}
	return abs
}

func jarDirectory(cfg Config, cfgPath string) string {
	jarPath := resolveConfigPath(cfgPath, cfg.JarPath)
	if jarPath == "" {
		return configDir(cfgPath)
	}
	return filepath.Dir(jarPath)
}

func resolveDataDirectory(cfg Config, cfgPath string) string {
	value := strings.TrimSpace(cfg.DataDirectory)
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(jarDirectory(cfg, cfgPath), value))
}

func portableDataDirectory(cfg Config, cfgPath, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return filepath.Clean(value)
	}
	rel, err := filepath.Rel(jarDirectory(cfg, cfgPath), abs)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel
	}
	return abs
}

func defaultConfigPath() string {
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		// `go run` puts the executable in a temporary go-build directory. Keeping
		// its config there is surprising, so development runs use the cwd.
		if !strings.Contains(filepath.ToSlash(dir), "/go-build") {
			return filepath.Join(dir, configFileName)
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return filepath.Join(cwd, configFileName)
}
