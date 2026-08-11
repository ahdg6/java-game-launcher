package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type LaunchSpec struct {
	InstanceID         string
	Java               JavaCandidate
	Jar                JarInfo
	WorkingDir         string
	DataDirectory      string
	NeedsGraphics      bool
	InteractiveConsole bool
	Args               []string
	Command            *exec.Cmd
}

func prepareLaunch(cfg Config, cfgPath string) (LaunchSpec, error) {
	javaPath := resolveConfigPath(cfgPath, cfg.JavaPath)
	jarPath := resolveConfigPath(cfgPath, cfg.JarPath)
	if cfg.JavaPath == "" {
		return LaunchSpec{}, fmt.Errorf("尚未选择 Java；请先重新检测或填写 Java 路径")
	}
	if cfg.JarPath == "" {
		return LaunchSpec{}, fmt.Errorf("尚未选择游戏 JAR")
	}
	jar := inspectJarForProfile(jarPath, cfg.GameProfile)
	if jar.Err != nil {
		return LaunchSpec{}, fmt.Errorf("游戏 JAR 无效: %w", jar.Err)
	}
	java, probeErr := probeJava(javaPath)
	java.Path = javaPath
	if err := javaArchitectureError(java, jar); err != nil {
		return LaunchSpec{}, err
	}
	if probeErr != nil {
		return LaunchSpec{}, fmt.Errorf("Java 无效: %w", probeErr)
	}
	if jar.RequiredJavaVersion > 0 && java.Version < jar.RequiredJavaVersion {
		return LaunchSpec{}, fmt.Errorf("游戏至少需要 Java %d，当前是 Java %d (%s)", jar.RequiredJavaVersion, java.Version, java.VersionText)
	}
	for i, arg := range append(append([]string{}, cfg.JVMArgs...), cfg.GameArgs...) {
		if strings.ContainsRune(arg, '\x00') {
			return LaunchSpec{}, fmt.Errorf("第 %d 个参数包含无效的 NUL 字符", i+1)
		}
	}
	jvmArgs, _ := normalizeDataDirectoryArg(cfg.JVMArgs, cfg.DataDirectory, configVersion)
	args := append([]string{}, jvmArgs...)
	adapter := effectiveAdapter(cfg, jar)
	if requiredModules := adapter.RequiredJavaModules(jar.MainClass); len(requiredModules) > 0 {
		modules, moduleErr := probeJavaModules(javaPath)
		if moduleErr != nil {
			return LaunchSpec{}, fmt.Errorf("检查 Java 模块: %w", moduleErr)
		}
		if missing := missingJavaModules(modules, requiredModules); len(missing) > 0 {
			return LaunchSpec{}, fmt.Errorf("Java 运行时缺少游戏必需模块：%s；请换用完整的 JRE/JDK", strings.Join(missing, ", "))
		}
	}
	dataDirectory := resolveDataDirectory(cfg, cfgPath)
	dataProperty := adapter.DataDirectoryProperty()
	if dataDirectory != "" && dataProperty != "" {
		if info, statErr := os.Stat(dataDirectory); statErr == nil && !info.IsDir() {
			return LaunchSpec{}, fmt.Errorf("数据目录路径指向了文件: %s", dataDirectory)
		} else if statErr != nil && !os.IsNotExist(statErr) {
			return LaunchSpec{}, fmt.Errorf("检查数据目录: %w", statErr)
		}
	} else if dataProperty == "" {
		dataDirectory = ""
	}
	adapterJVMArgs, adapterGameArgs, err := adapter.LaunchArguments(AdapterLaunchContext{
		Config: cfg, Jar: jar, Java: java, DataDirectory: dataDirectory,
	})
	if err != nil {
		return LaunchSpec{}, fmt.Errorf("%s 游戏配置生成启动参数: %w", adapter.DisplayName(), err)
	}
	args = append(args, adapterJVMArgs...)
	args = append(args, "-jar", jarPath)
	args = append(args, cfg.GameArgs...)
	args = append(args, adapterGameArgs...)
	workingDir := strings.TrimSpace(cfg.WorkingDirectory)
	if workingDir == "" {
		workingDir = filepath.Dir(jarPath)
	} else {
		workingDir = resolveConfigPath(cfgPath, workingDir)
	}
	info, err := os.Stat(workingDir)
	if err != nil || !info.IsDir() {
		return LaunchSpec{}, fmt.Errorf("工作目录不可用: %s", workingDir)
	}
	needsGraphics := adapter.NeedsGraphics(jar.MainClass)
	interactiveConsole := adapter.InteractiveConsole(jar.MainClass)
	cmd := javaCommand(javaPath, args, workingDir, needsGraphics)
	cmd.Dir = workingDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return LaunchSpec{
		InstanceID: cfg.InstanceID,
		Java:       java,
		Jar:        jar, WorkingDir: workingDir, DataDirectory: dataDirectory,
		NeedsGraphics: needsGraphics, InteractiveConsole: interactiveConsole,
		Args: args, Command: cmd,
	}, nil
}

func ensureLaunchDirectories(spec LaunchSpec) error {
	if spec.DataDirectory == "" {
		return nil
	}
	if err := os.MkdirAll(spec.DataDirectory, 0o755); err != nil {
		return fmt.Errorf("创建数据目录 %s: %w", spec.DataDirectory, err)
	}
	return nil
}

func formatCommand(spec LaunchSpec) string {
	parts := make([]string, 0, len(spec.Command.Args))
	for _, arg := range spec.Command.Args {
		parts = append(parts, quoteArg(arg))
	}
	return strings.Join(parts, " ")
}

func javaCommand(javaPath string, args []string, workingDir string, needsGraphics bool) *exec.Cmd {
	if needsGraphics && shouldLaunchOnFlatpakHost() {
		if spawn, err := exec.LookPath("flatpak-spawn"); err == nil {
			spawnArgs := []string{"--host", "--watch-bus", "--directory=" + workingDir, javaPath}
			spawnArgs = append(spawnArgs, args...)
			return exec.Command(spawn, spawnArgs...)
		}
	}
	return exec.Command(javaPath, args...)
}

func shouldLaunchOnFlatpakHost() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if _, err := os.Stat("/.flatpak-info"); err == nil {
		return true
	}
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		if _, err := os.Stat(filepath.Join(runtimeDir, "flatpak-info")); err == nil {
			return true
		}
	}
	return false
}

func quoteArg(arg string) string {
	if arg != "" && !strings.ContainsAny(arg, " \t\r\n\"'") {
		return arg
	}
	return `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
}
