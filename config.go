package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

const configVersion = 4

const (
	configFileName       = "java-game-launcher.json"
	legacyConfigFileName = "mindustry-launcher.json"
)

type Config struct {
	Version          int      `json:"version"`
	GameProfile      string   `json:"game_profile"`
	JavaPath         string   `json:"java_path"`
	JarPath          string   `json:"jar_path"`
	WorkingDirectory string   `json:"working_directory,omitempty"`
	DataDirectory    string   `json:"data_directory"`
	JVMArgs          []string `json:"jvm_args"`
	GameArgs         []string `json:"game_args"`
}

func defaultConfig() Config {
	return Config{
		Version:       configVersion,
		GameProfile:   profileAuto,
		DataDirectory: "game_data",
		JVMArgs:       defaultJVMArgs(),
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
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("读取配置: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("解析配置 %s: %w", path, err)
	}
	loadedVersion := cfg.Version
	if cfg.GameProfile == "" {
		cfg.GameProfile = profileAuto
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
	cfg.Version = configVersion
	cfg.JVMArgs, cfg.DataDirectory = normalizeDataDirectoryArg(cfg.JVMArgs, cfg.DataDirectory, configVersion)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("编码配置: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建配置目录: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入临时配置: %w", err)
	}
	if runtime.GOOS == "windows" {
		_ = os.Remove(path)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("替换配置: %w", err)
	}
	return nil
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
