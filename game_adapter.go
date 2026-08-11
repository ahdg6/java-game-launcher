package main

import (
	"archive/zip"
	"runtime"
	"strings"
)

const (
	profileAuto      = "auto"
	profileGeneric   = "generic"
	profileMindustry = "mindustry"
)

// GameAdapter isolates behavior that is not universal to executable Java
// archives. Adding support for another game should happen here rather than in
// Java discovery, process management, or the TUI.
type GameAdapter interface {
	ID() string
	DisplayName() string
	Matches(mainClass string) bool
	DataDirectoryProperty() string
	RequiredJavaModules(mainClass string) []string
	NeedsGraphics(mainClass string) bool
	InteractiveConsole(mainClass string) bool
	NativeArchitectures(files []*zip.File, goos string) []string
	LaunchArguments(context AdapterLaunchContext) (jvmArgs, gameArgs []string, err error)
}

type AdapterLaunchContext struct {
	Config        Config
	Jar           JarInfo
	Java          JavaCandidate
	DataDirectory string
}

type genericAdapter struct{}

func (genericAdapter) ID() string                                       { return profileGeneric }
func (genericAdapter) DisplayName() string                              { return "通用 Java 游戏" }
func (genericAdapter) Matches(string) bool                              { return false }
func (genericAdapter) DataDirectoryProperty() string                    { return "" }
func (genericAdapter) RequiredJavaModules(string) []string              { return nil }
func (genericAdapter) NeedsGraphics(string) bool                        { return true }
func (genericAdapter) InteractiveConsole(string) bool                   { return false }
func (genericAdapter) NativeArchitectures([]*zip.File, string) []string { return nil }
func (genericAdapter) LaunchArguments(AdapterLaunchContext) ([]string, []string, error) {
	return nil, nil, nil
}

type mindustryAdapter struct{}

func (mindustryAdapter) ID() string          { return profileMindustry }
func (mindustryAdapter) DisplayName() string { return "Mindustry" }
func (mindustryAdapter) Matches(mainClass string) bool {
	return strings.HasPrefix(mainClass, "mindustry.")
}
func (mindustryAdapter) DataDirectoryProperty() string { return "mindustry.data.dir" }
func (mindustryAdapter) RequiredJavaModules(string) []string {
	return []string{"java.desktop", "jdk.unsupported"}
}
func (mindustryAdapter) NeedsGraphics(mainClass string) bool {
	return !strings.Contains(mainClass, ".server.") && !strings.HasSuffix(mainClass, "ServerLauncher")
}
func (mindustryAdapter) InteractiveConsole(mainClass string) bool {
	return strings.Contains(mainClass, ".server.") || strings.HasSuffix(mainClass, "ServerLauncher")
}
func (mindustryAdapter) NativeArchitectures(files []*zip.File, goos string) []string {
	return arcNativeArchitectures(files, goos)
}
func (mindustryAdapter) LaunchArguments(context AdapterLaunchContext) ([]string, []string, error) {
	if context.DataDirectory == "" {
		return nil, nil, nil
	}
	return []string{"-Dmindustry.data.dir=" + context.DataDirectory}, nil, nil
}

var gameAdapters = []GameAdapter{
	mindustryAdapter{},
}

func resolveGameAdapter(requested, mainClass string) GameAdapter {
	if requested != "" && requested != profileAuto {
		for _, adapter := range gameAdapters {
			if adapter.ID() == requested {
				return adapter
			}
		}
		if requested == profileGeneric {
			return genericAdapter{}
		}
	}
	for _, adapter := range gameAdapters {
		if adapter.Matches(mainClass) {
			return adapter
		}
	}
	return genericAdapter{}
}

func configuredProfileIDs() []string {
	return []string{profileAuto, profileGeneric, profileMindustry}
}

func profileDisplayName(configured, mainClass string) string {
	adapter := resolveGameAdapter(configured, mainClass)
	if configured == "" || configured == profileAuto {
		return "自动（" + adapter.DisplayName() + "）"
	}
	return adapter.DisplayName()
}

func arcNativeArchitectures(files []*zip.File, goos string) []string {
	wanted := map[string]string{}
	switch goos {
	case "windows":
		wanted = map[string]string{
			"arc.dll": "x86", "arc64.dll": "amd64", "arcarm64.dll": "arm64",
		}
	case "linux":
		wanted = map[string]string{
			"libarc.so": "x86", "libarc64.so": "amd64", "libarcarm64.so": "arm64",
		}
	case "darwin":
		wanted = map[string]string{
			"libarc.dylib": "x86", "libarc64.dylib": "amd64", "libarcarm64.dylib": "arm64",
		}
	}
	seen := map[string]bool{}
	architectures := []string{}
	for _, file := range files {
		if arch, ok := wanted[file.Name]; ok && !seen[arch] {
			seen[arch] = true
			architectures = append(architectures, arch)
		}
	}
	return architectures
}

func effectiveAdapter(cfg Config, jar JarInfo) GameAdapter {
	return resolveGameAdapter(cfg.GameProfile, jar.MainClass)
}

func currentPlatform() string { return runtime.GOOS }
