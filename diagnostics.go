package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// DiagnosticSeverity describes how strongly a diagnostic affects launching.
// The string values are intentionally stable so the TUI can choose its own
// localized label and style without parsing diagnostic text.
type DiagnosticSeverity string

const (
	DiagnosticSeverityError   DiagnosticSeverity = "error"
	DiagnosticSeverityWarning DiagnosticSeverity = "warning"
	DiagnosticSeverityInfo    DiagnosticSeverity = "info"
)

// Diagnostic is an actionable explanation inferred from a failed Java launch.
// Code and Severity are machine-readable; the remaining fields are intended to
// be shown directly to Chinese-speaking users.
type Diagnostic struct {
	Code        string
	Severity    DiagnosticSeverity
	Title       string
	Summary     string
	Suggestions []string
}

var (
	classVersionPattern   = regexp.MustCompile(`(?i)class file version\s+([0-9]+)(?:\.[0-9]+)?.*?(?:only recognizes class file versions up to|up to)\s+([0-9]+)(?:\.[0-9]+)?`)
	moduleNotFoundPattern = regexp.MustCompile(`(?im)(?:module|模块)\s+([a-zA-Z0-9_.-]+)\s+(?:not found|未找到)`)
	classNotFoundPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?m)(?:ClassNotFoundException|NoClassDefFoundError):\s*([^\s:]+)`),
		regexp.MustCompile(`(?im)could not find or load main class\s+([^\s]+)`),
	}
	unrecognizedVMOptionPattern = regexp.MustCompile(`(?im)(?:unrecognized vm option|unrecognized option|improperly specified vm option)\s*[:=]?\s*['"]?([^'"\r\n]+)`)
)

// AnalyzeLaunchFailure extracts actionable diagnostics from the captured
// stdout/stderr and the process error. It is deterministic and has no side
// effects, which makes it safe to call whenever the log view is refreshed.
func AnalyzeLaunchFailure(output string, processErr error) []Diagnostic {
	combined := output
	if processErr != nil {
		if combined != "" && !strings.HasSuffix(combined, "\n") {
			combined += "\n"
		}
		combined += processErr.Error()
	}
	lower := strings.ToLower(combined)
	diagnostics := make([]Diagnostic, 0, 4)
	seen := make(map[string]struct{})
	add := func(d Diagnostic) {
		if _, exists := seen[d.Code]; exists {
			return
		}
		seen[d.Code] = struct{}{}
		diagnostics = append(diagnostics, d)
	}

	if summary, ok := diagnoseClassVersion(combined, lower); ok {
		add(Diagnostic{
			Code:     "java_version_too_old",
			Severity: DiagnosticSeverityError,
			Title:    "Java 版本过低",
			Summary:  summary,
			Suggestions: []string{
				"改用游戏要求版本或更高版本的 64 位 Java；Mindustry 建议优先使用 Java 17 或 21 LTS。",
				"在启动器中重新检测 Java，并确认选中的不是旧 JRE。",
			},
		})
	}

	architectureMismatch := hasArchitectureMismatch(lower)
	if architectureMismatch {
		add(Diagnostic{
			Code:     "java_architecture_mismatch",
			Severity: DiagnosticSeverityError,
			Title:    "Java 与游戏原生库架构不匹配",
			Summary:  "当前 Java 的位数或 CPU 架构与游戏携带的原生库不一致，因此原生库无法载入。",
			Suggestions: []string{
				"在常见的 x86-64 Windows/Linux 电脑上选择 amd64、64 位 Java，不要使用 x86、32 位 Java。",
				"ARM 设备应选择游戏确实包含的原生库架构；Windows ARM 上的游戏若只带 x64 DLL，请使用 x64 Java 兼容层。",
			},
		})
	}

	if match := unrecognizedVMOptionPattern.FindStringSubmatch(combined); len(match) > 0 {
		option := strings.TrimSpace(match[1])
		summary := "Java 不认识当前配置中的某个 JVM 参数，虚拟机尚未启动。"
		if option != "" {
			summary = fmt.Sprintf("Java 不支持或无法解析 JVM 参数 %q，虚拟机尚未启动。", option)
		}
		add(Diagnostic{
			Code:     "unrecognized_jvm_option",
			Severity: DiagnosticSeverityError,
			Title:    "JVM 参数无效",
			Summary:  summary,
			Suggestions: []string{
				"在 JVM 参数设置中删除该参数，或恢复启动器的默认优化参数。",
				"如果参数来自旧版 Java，请查询当前 Java 版本是否已移除或改名该选项。",
			},
		})
	}

	if hasMemoryFailure(lower) {
		startup := containsAny(lower,
			"could not reserve enough space", "could not allocate compressed class space",
			"insufficient memory for the java runtime environment", "not enough space for object heap",
			"failed to reserve memory", "unable to allocate memory for the java virtual machine")
		summary := "游戏或 Java 运行时耗尽了可用内存。"
		if startup {
			summary = "Java 无法为当前堆内存设置预留足够空间，因此游戏尚未启动。"
		}
		add(Diagnostic{
			Code:     "java_out_of_memory",
			Severity: DiagnosticSeverityError,
			Title:    "Java 内存不足",
			Summary:  summary,
			Suggestions: []string{
				"调低 -Xms 和 -Xmx（例如先尝试 -Xms1g -Xmx2g），并关闭占用内存较多的程序。",
				"确认正在使用 64 位 Java；32 位 Java 无法提供较大的堆内存。",
				"若游戏运行一段时间后才出现 OOM，请排查大型模组、超大地图或资源泄漏。",
			},
		})
	}

	missingModule := ""
	if match := moduleNotFoundPattern.FindStringSubmatch(combined); len(match) > 1 {
		missingModule = strings.TrimSpace(match[1])
	}
	if missingModule != "" {
		add(Diagnostic{
			Code:     "missing_java_module",
			Severity: DiagnosticSeverityError,
			Title:    "Java 运行时缺少模块",
			Summary:  fmt.Sprintf("当前 Java 运行时不包含游戏需要的模块 %q，可能是经过裁剪的 jlink 运行时。", missingModule),
			Suggestions: []string{
				"改用完整发行版的 OpenJDK/JRE，而不是精简或自行裁剪的运行时。",
				"Mindustry 通常需要完整运行时中的 java.desktop 和 jdk.unsupported 模块。",
			},
		})
	}

	// A missing module often causes a secondary NoClassDefFoundError. Showing
	// both would merely repeat the same root cause, so prefer the module result.
	if missingModule == "" {
		if className := findMissingClass(combined); className != "" {
			add(Diagnostic{
				Code:     "missing_java_class",
				Severity: DiagnosticSeverityError,
				Title:    "缺少 Java 类或游戏依赖",
				Summary:  fmt.Sprintf("启动时找不到类 %q；游戏包、依赖或 Java 运行时可能不完整。", className),
				Suggestions: []string{
					"重新下载完整的游戏 JAR，避免只复制拆分发行包中的单个文件。",
					"若缺少的是 java.* 或 javax.* 类，请改用完整的标准 Java 运行时。",
					"若使用模组加载器，请确认其依赖、版本和启动入口均正确。",
				},
			})
		}
	}

	if containsAny(lower, "no available video device", "failed to initialize video device") {
		add(Diagnostic{
			Code:     "graphics_environment_unavailable",
			Severity: DiagnosticSeverityError,
			Title:    "无法连接图形显示环境",
			Summary:  "SDL 找不到可用的视频设备；当前终端、容器或沙箱可能访问不到桌面会话。",
			Suggestions: []string{
				"从已登录的桌面会话启动，并检查 Linux 下的 DISPLAY、WAYLAND_DISPLAY 和 XDG_RUNTIME_DIR。",
				"若在 Flatpak/容器中运行启动器，请授予 X11/Wayland 权限，或让游戏进程在宿主环境运行。",
				"远程 SSH 会话需要正确配置图形转发；纯服务器环境不能直接启动桌面客户端。",
			},
		})
	}

	if hasFilesystemPermissionFailure(lower) {
		add(Diagnostic{
			Code:     "filesystem_permission_denied",
			Severity: DiagnosticSeverityError,
			Title:    "数据目录或文件权限不足",
			Summary:  "游戏无法创建或读写所需文件，数据目录、临时目录或游戏目录可能不可写。",
			Suggestions: []string{
				"在启动器中把数据目录改到当前用户可读写的位置，并确认目录确实存在或允许创建。",
				"不要把数据目录放在只读介质、受保护的系统目录或无写权限的共享目录中。",
				"Linux/macOS 可检查目录所有者与权限；Windows 可检查安全权限和杀毒软件拦截记录。",
			},
		})
	}

	// Architecture errors are a specific kind of native-library failure. Once
	// identified, avoid adding a second, less useful native-library diagnostic.
	if !architectureMismatch && hasNativeLibraryFailure(lower) {
		add(Diagnostic{
			Code:     "native_library_load_failed",
			Severity: DiagnosticSeverityError,
			Title:    "原生库加载失败",
			Summary:  "游戏需要的 DLL、SO 或 dylib 原生库无法提取或载入，文件可能缺失、损坏或被系统阻止。",
			Suggestions: []string{
				"重新下载完整游戏包，并避免直接从压缩包内运行启动器或 JAR。",
				"确认临时目录和数据目录可写，并检查杀毒软件是否隔离了原生库。",
				"Linux 可检查缺失的系统动态库；Windows 可安装游戏所需的 Microsoft Visual C++ 运行库。",
			},
		})
	}

	if processErr != nil && len(diagnostics) == 0 {
		add(Diagnostic{
			Code:     "process_nonzero_exit",
			Severity: DiagnosticSeverityError,
			Title:    "游戏进程异常退出",
			Summary:  fmt.Sprintf("游戏没有正常结束（%s），但日志中没有识别出明确原因。", conciseError(processErr)),
			Suggestions: []string{
				"查看完整启动日志中最早出现的 Exception、Error 或 Caused by。",
				"尝试恢复默认 JVM 参数、暂时禁用模组，并确认游戏 JAR 与 Java 版本匹配。",
				"反馈问题时请附上完整日志、操作系统、Java 版本和游戏版本。",
			},
		})
	}

	sort.SliceStable(diagnostics, func(i, j int) bool {
		return severityRank(diagnostics[i].Severity) < severityRank(diagnostics[j].Severity)
	})
	return diagnostics
}

func diagnoseClassVersion(original, lower string) (string, bool) {
	if !strings.Contains(lower, "unsupportedclassversionerror") &&
		!strings.Contains(lower, "compiled by a more recent version of the java runtime") &&
		!strings.Contains(lower, "class file version") {
		return "", false
	}
	match := classVersionPattern.FindStringSubmatch(original)
	if len(match) < 3 {
		return "游戏由更高版本的 Java 编译，当前 Java 无法读取它的 class 文件。", true
	}
	requiredMajor, requiredErr := strconv.Atoi(match[1])
	currentMajor, currentErr := strconv.Atoi(match[2])
	if requiredErr != nil || currentErr != nil {
		return "游戏由更高版本的 Java 编译，当前 Java 无法读取它的 class 文件。", true
	}
	requiredJava := javaVersionForClassMajor(requiredMajor)
	currentJava := javaVersionForClassMajor(currentMajor)
	if requiredJava > 0 && currentJava > 0 {
		return fmt.Sprintf("游戏需要 Java %d（class %d），当前运行时最高只支持 Java %d（class %d）。", requiredJava, requiredMajor, currentJava, currentMajor), true
	}
	return fmt.Sprintf("游戏需要 class 文件版本 %d，当前运行时最高只支持 %d。", requiredMajor, currentMajor), true
}

func javaVersionForClassMajor(major int) int {
	switch {
	case major == 45:
		return 1
	case major >= 46 && major <= 48:
		return major - 44
	case major >= 49:
		return major - 44
	default:
		return 0
	}
}

func hasArchitectureMismatch(lower string) bool {
	if containsAny(lower,
		"wrong elf class", "wrong architecture", "incompatible architecture",
		"can't load amd 64-bit", "cannot load amd 64-bit", "ia 32-bit platform",
		"%1 is not a valid win32 application", "not a valid win32 application",
		"bad cpu type in executable", "exec format error") {
		return true
	}
	// Arc reports the JVM-derived target. A missing 32-bit Arc library in a
	// package that normally ships 64-bit natives is the common Mindustry case.
	return containsAny(lower, "for target: linux, 32-bit", "for target: windows, 32-bit", "for target: mac os x, 32-bit") &&
		containsAny(lower, "couldn't load shared library", "could not load shared library", "unable to read file for extraction")
}

func hasMemoryFailure(lower string) bool {
	return containsAny(lower,
		"java.lang.outofmemoryerror", "outofmemoryerror:",
		"could not reserve enough space", "not enough space for object heap",
		"could not allocate compressed class space", "insufficient memory for the java runtime environment",
		"failed to reserve memory", "unable to allocate memory for the java virtual machine",
		"native memory allocation (malloc) failed")
}

func findMissingClass(original string) string {
	for _, pattern := range classNotFoundPatterns {
		match := pattern.FindStringSubmatch(original)
		if len(match) < 2 {
			continue
		}
		name := strings.Trim(strings.TrimSpace(match[1]), "'\"")
		if strings.EqualFold(name, "could") { // NoClassDefFoundError: Could not initialize class ...
			continue
		}
		return strings.ReplaceAll(name, "/", ".")
	}
	return ""
}

func hasFilesystemPermissionFailure(lower string) bool {
	return containsAny(lower,
		"permission denied", "accessdeniedexception", "access is denied",
		"operation not permitted", "read-only file system", "readonlyfilesystemexception",
		"拒绝访问", "权限不足", "只读文件系统")
}

func hasNativeLibraryFailure(lower string) bool {
	return containsAny(lower,
		"unsatisfiedlinkerror", "couldn't load shared library", "could not load shared library",
		"unable to load shared library", "failed to load native library", "native library load failed",
		"unable to read file for extraction", " in java.library.path", "failed to map segment from shared object",
		"image not found")
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func conciseError(err error) string {
	if err == nil {
		return "未知错误"
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "未知错误"
	}
	if line, _, ok := strings.Cut(message, "\n"); ok {
		return line
	}
	return message
}

func severityRank(severity DiagnosticSeverity) int {
	switch severity {
	case DiagnosticSeverityError:
		return 0
	case DiagnosticSeverityWarning:
		return 1
	case DiagnosticSeverityInfo:
		return 2
	default:
		return 3
	}
}
