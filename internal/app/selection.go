package app

import (
	"fmt"
	"path/filepath"

	"github.com/ahdg6/java-game-launcher/internal/java"
	tea "github.com/charmbracelet/bubbletea"
)

func applyAutoSelections(cfg *Config, cfgPath string, env Environment) bool {
	changed := false
	jar, jarOK := findSelectedJar(*cfg, cfgPath, env)
	if !jarOK || jar.Err != nil {
		if candidate, ok := selectJar(env.Jars); ok {
			cfg.JarPath = portablePath(cfgPath, candidate.Path)
			jar, jarOK = candidate, true
			changed = true
		}
	}
	required := 0
	if jarOK && jar.Err == nil {
		required = jar.RequiredJavaVersion
	}
	java, javaOK := findSelectedJava(*cfg, cfgPath, env)
	if !javaOK || java.Err != nil || (required > 0 && java.Version < required) {
		if candidate, ok := selectJava(env.Java, required); ok {
			cfg.JavaPath = portablePath(cfgPath, candidate.Path)
			changed = true
		}
	}
	return changed
}

func selectedJava(cfg Config, cfgPath string, env Environment) bool {
	java, ok := findSelectedJava(cfg, cfgPath, env)
	return ok && java.Err == nil && java.Version >= selectedRequiredJava(cfg, cfgPath, env)
}

func selectedJar(cfg Config, cfgPath string, env Environment) bool {
	jar, ok := findSelectedJar(cfg, cfgPath, env)
	return ok && jar.Err == nil
}

func selectedRequiredJava(cfg Config, cfgPath string, env Environment) int {
	jar, ok := findSelectedJar(cfg, cfgPath, env)
	if ok && jar.Err == nil {
		return jar.RequiredJavaVersion
	}
	return 0
}

func findSelectedJava(cfg Config, cfgPath string, env Environment) (JavaCandidate, bool) {
	current := resolveConfigPath(cfgPath, cfg.JavaPath)
	if cfg.JavaPath == "" {
		return JavaCandidate{}, false
	}
	for _, candidate := range env.Java {
		if pathKey(candidate.Path) == pathKey(current) {
			return candidate, true
		}
	}
	return JavaCandidate{}, false
}

func findSelectedJar(cfg Config, cfgPath string, env Environment) (JarInfo, bool) {
	current := resolveConfigPath(cfgPath, cfg.JarPath)
	if cfg.JarPath == "" {
		return JarInfo{}, false
	}
	for _, candidate := range env.Jars {
		if pathKey(candidate.Path) == pathKey(current) {
			return candidate, true
		}
	}
	return JarInfo{}, false
}

func (m *model) cycleJava(delta int) {
	required := selectedRequiredJava(m.cfg, m.cfgPath, m.env)
	valid := make([]JavaCandidate, 0, len(m.env.Java))
	for _, candidate := range m.env.Java {
		if candidate.Err == nil && (required == 0 || candidate.Version >= required) {
			valid = append(valid, candidate)
		}
	}
	if len(valid) == 0 {
		m.setStatus("没有可切换的兼容 Java", true)
		return
	}
	current := resolveConfigPath(m.cfgPath, m.cfg.JavaPath)
	index := 0
	for i, candidate := range valid {
		if pathKey(candidate.Path) == pathKey(current) {
			index = i
			break
		}
	}
	index = (index + delta + len(valid)) % len(valid)
	m.cfg.JavaPath = portablePath(m.cfgPath, valid[index].Path)
	m.dirty = true
	m.setStatus(fmt.Sprintf("已选择 Java %d（%s）", valid[index].Version, valid[index].Source), false)
}

func (m *model) cycleJar(delta int) {
	valid := make([]JarInfo, 0, len(m.env.Jars))
	for _, candidate := range m.env.Jars {
		if candidate.Err == nil {
			valid = append(valid, candidate)
		}
	}
	if len(valid) == 0 {
		m.setStatus("没有可切换的游戏 JAR", true)
		return
	}
	current := resolveConfigPath(m.cfgPath, m.cfg.JarPath)
	index := 0
	for i, candidate := range valid {
		if pathKey(candidate.Path) == pathKey(current) {
			index = i
			break
		}
	}
	index = (index + delta + len(valid)) % len(valid)
	m.cfg.JarPath = portablePath(m.cfgPath, valid[index].Path)
	m.dirty = true
	m.setStatus(fmt.Sprintf("已选择 %s（需要 Java %d+）", filepath.Base(valid[index].Path), valid[index].RequiredJavaVersion), false)
}

func (m *model) cycleProfile(delta int) (tea.Model, tea.Cmd) {
	profiles := configuredProfileIDs()
	current := m.cfg.GameProfile
	if current == "" {
		current = profileAuto
	}
	index := 0
	for i, profile := range profiles {
		if profile == current {
			index = i
			break
		}
	}
	index = (index + delta + len(profiles)) % len(profiles)
	m.cfg.GameProfile = profiles[index]
	m.dirty = true
	m.loading = true
	m.discoveryGeneration++
	m.setStatus("正在按新的游戏配置重新检查 JAR 与 Java…", false)
	return *m, discoverCmd(m.cfg, m.cfgPath, m.discoveryGeneration)
}

func (m *model) cycleJVMPreset(delta int) {
	presets := java.AvailableJVMPresets(m.memory)
	current := m.cfg.JVMPreset
	index := 0
	found := false
	for i, preset := range presets {
		if preset.ID == current {
			index = i
			found = true
			break
		}
	}
	if !found {
		index = 0
	} else {
		index = (index + delta + len(presets)) % len(presets)
	}
	preset := presets[index]
	m.cfg.JVMPreset = preset.ID
	m.cfg.JVMArgs = preset.Args
	m.dirty = true
	m.setStatus("已选择 "+preset.Name+"："+preset.Description, false)
}
