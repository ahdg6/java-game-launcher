package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const configVersion = 6

const presetCustom = "custom"

const (
	configFileName       = "java-game-launcher.json"
	legacyConfigFileName = "mindustry-launcher.json"
)

type Config struct {
	// Version and InstanceID belong to the compatibility view used by the
	// launcher and the current TUI. v6 persists them on LauncherConfig and
	// InstanceConfig respectively instead of duplicating them in every instance.
	Version          int      `json:"version,omitempty"`
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
		Version:       configVersion,
		InstanceID:    defaultInstanceID,
		GameProfile:   profileAuto,
		DataDirectory: "game_data",
		JVMPreset:     presetAuto,
		JVMArgs:       ResolveJVMPreset(presetAuto, DetectMemory()).Args,
		GameArgs:      []string{},
	}
}

// defaultJVMArgs favors stable frame times on Java 17+. A fixed-size heap
// avoids heap expansion during play, G1 keeps collection pauses bounded, and
// pre-touch pays page-fault costs at startup instead of during the first match.
// Keep this list short: speculative flags can steal CPU time from rendering.
func defaultJVMArgs() []string {
	return []string{
		"-Xms2g",
		"-Xmx2g",
		"-XX:+UseG1GC",
		"-XX:MaxGCPauseMillis=30",
		"-XX:G1ReservePercent=20",
		"-XX:+ParallelRefProcEnabled",
		"-XX:+AlwaysPreTouch",
		"-XX:+DisableExplicitGC",
		"-Dfile.encoding=UTF-8",
	}
}

func legacyDefaultJVMArgs() []string {
	return []string{
		"-Xms512m",
		"-Xmx2g",
		"-XX:+UseG1GC",
		"-XX:MaxGCPauseMillis=50",
		"-XX:+ParallelRefProcEnabled",
		"-XX:+UseStringDeduplication",
		"-Dfile.encoding=UTF-8",
	}
}

func loadConfig(path string) (Config, error) {
	launcher, err := loadLauncherConfig(path)
	if err != nil {
		return defaultConfig(), err
	}
	active, err := launcher.Active()
	if err != nil {
		return defaultConfig(), err
	}
	return active.Config(), nil
}

func decodeLegacyConfig(data []byte) (Config, error) {
	cfg := defaultConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	loadedVersion := cfg.Version
	if cfg.GameProfile == "" {
		cfg.GameProfile = profileAuto
	}
	if loadedVersion < 5 {
		cfg.JVMPreset = presetCustom
	} else if cfg.JVMPreset == "" {
		cfg.JVMPreset = presetAuto
	}
	if loadedVersion < 3 {
		cfg.DataDirectory = "game_data"
	}
	if cfg.JVMArgs == nil {
		cfg.JVMArgs = defaultConfig().JVMArgs
	}
	cfg.JVMArgs, cfg.DataDirectory = normalizeDataDirectoryArg(cfg.JVMArgs, cfg.DataDirectory, loadedVersion)
	if loadedVersion < configVersion {
		if slices.Equal(cfg.JVMArgs, legacyDefaultJVMArgs()) {
			cfg.JVMArgs = defaultJVMArgs()
		}
		cfg.Version = configVersion
	}
	if cfg.GameArgs == nil {
		cfg.GameArgs = []string{}
	}
	if cfg.JVMPreset != presetCustom {
		preset := ResolveJVMPreset(cfg.JVMPreset, DetectMemory())
		cfg.JVMPreset = preset.ID
		cfg.JVMArgs = preset.Args
	}
	return cfg, nil
}

func normalizeDataDirectoryArg(args []string, dataDirectory string, loadedVersion int) ([]string, string) {
	const prefix = "-Dmindustry.data.dir="
	clean := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			// Before v3 this setting only existed as a hand-written JVM argument.
			// Migrate it into the dedicated field. In v3+, the dedicated field is
			// authoritative so clearing it really means no property is passed.
			if loadedVersion < 3 {
				dataDirectory = strings.TrimPrefix(arg, prefix)
			}
			continue
		}
		clean = append(clean, arg)
	}
	return clean, dataDirectory
}

func saveConfig(path string, cfg Config) error {
	launcher, err := loadLauncherConfig(path)
	if err != nil {
		if _, statErr := os.Stat(path); statErr == nil {
			return err
		}
		launcher = defaultLauncherConfig()
	}
	instance := launcher.InstanceByID(cfg.InstanceID)
	if instance == nil {
		if cfg.InstanceID != "" {
			return fmt.Errorf("保存配置：实例 %q 不存在", cfg.InstanceID)
		}
		instance, err = launcher.Active()
		if err != nil {
			return err
		}
	}
	instance.ApplyConfig(cfg)
	return saveLauncherConfig(path, launcher)
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
