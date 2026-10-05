package java

import (
	"context"
	"debug/elf"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Runtime describes a verified Java executable.
type Runtime struct {
	Path         string
	Version      int
	VersionText  string
	Architecture string
	DataModel    int
	Vendor       string
	JavaHome     string
}

var versionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)version\s+"([^"]+)"`),
	regexp.MustCompile(`(?i)(?:openjdk|java)\s+([0-9][^\s]*)`),
}

func javaExecutableName() string {
	if runtime.GOOS == "windows" {
		return "java.exe"
	}
	return "java"
}

// Probe executes a Java binary and reads its version and runtime properties.
func Probe(path string) (Runtime, error) {
	return ProbeContext(context.Background(), path)
}

// ProbeContext probes a runtime with a four-second deadline and caller cancellation.
func ProbeContext(ctx context.Context, path string) (Runtime, error) {
	result := Runtime{Path: path}
	result.Architecture, result.DataModel = executableArchitecture(path)
	info, err := os.Stat(path)
	if err != nil {
		return result, err
	}
	if info.IsDir() {
		return result, fmt.Errorf("路径是目录")
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, "-XshowSettings:properties", "-version")
	command.WaitDelay = time.Second
	out, err := command.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return result, fmt.Errorf("检测超时: %w", ctx.Err())
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	text := strings.TrimSpace(string(out))
	for _, pattern := range versionPatterns {
		if match := pattern.FindStringSubmatch(text); len(match) > 1 {
			result.VersionText = match[1]
			break
		}
	}
	result.Version = ParseMajor(result.VersionText)
	properties := ParseProperties(text)
	if arch := NormalizeArchitecture(properties["os.arch"]); arch != "" {
		result.Architecture = arch
	}
	if bits, parseErr := strconv.Atoi(properties["sun.arch.data.model"]); parseErr == nil {
		result.DataModel = bits
	}
	result.Vendor = properties["java.vendor"]
	result.JavaHome = properties["java.home"]
	if err != nil {
		return result, fmt.Errorf("执行 Java 探测: %w", err)
	}
	if result.Version == 0 {
		return result, fmt.Errorf("无法识别 Java 版本")
	}
	return result, nil
}

// ParseProperties parses the property lines printed by -XshowSettings.
func ParseProperties(output string) map[string]string {
	properties := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(parts) == 2 {
			properties[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return properties
}

// ProbeModules returns the names reported by java --list-modules.
func ProbeModules(path string) (map[string]bool, error) {
	return ProbeModulesContext(context.Background(), path)
}

// ProbeModulesContext lists modules with a four-second deadline and caller cancellation.
func ProbeModulesContext(ctx context.Context, path string) (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, "--list-modules")
	command.WaitDelay = time.Second
	out, err := command.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("模块检测超时: %w", ctx.Err())
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("执行 java --list-modules: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return ParseModules(string(out)), nil
}

// ParseModules parses java --list-modules output.
func ParseModules(output string) map[string]bool {
	modules := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		name := strings.TrimSpace(strings.SplitN(line, "@", 2)[0])
		if name != "" {
			modules[name] = true
		}
	}
	return modules
}

// MissingModules returns required module names absent from available.
func MissingModules(available map[string]bool, required []string) []string {
	missing := []string{}
	for _, module := range required {
		if !available[module] {
			missing = append(missing, module)
		}
	}
	return missing
}

func executableArchitecture(path string) (string, int) {
	file, err := elf.Open(path)
	if err != nil {
		return "", 0
	}
	defer file.Close()
	bits := 0
	if file.Class == elf.ELFCLASS32 {
		bits = 32
	} else if file.Class == elf.ELFCLASS64 {
		bits = 64
	}
	switch file.Machine {
	case elf.EM_386:
		return "x86", bits
	case elf.EM_X86_64:
		return "amd64", bits
	case elf.EM_AARCH64:
		return "arm64", bits
	case elf.EM_ARM:
		return "arm", bits
	default:
		return "", bits
	}
}

// NormalizeArchitecture maps common JVM architecture names to Go-style names.
func NormalizeArchitecture(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "amd64", "x86_64", "x64":
		return "amd64"
	case "x86", "i386", "i486", "i586", "i686":
		return "x86"
	case "aarch64", "arm64":
		return "arm64"
	case "arm", "arm32":
		return "arm"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

// ParseMajor returns the Java feature version from a runtime version string.
func ParseMajor(version string) int {
	version = strings.TrimSpace(strings.Trim(version, `"`))
	parts := strings.FieldsFunc(version, func(r rune) bool {
		return r == '.' || r == '-' || r == '_' || r == '+'
	})
	if len(parts) == 0 {
		return 0
	}
	first, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	if first == 1 && len(parts) > 1 {
		second, _ := strconv.Atoi(parts[1])
		return second
	}
	return first
}
