package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	configPath := flag.String("config", defaultConfigPath(), "配置文件路径")
	launch := flag.Bool("launch", false, "不进入 TUI，直接启动游戏")
	dryRun := flag.Bool("dry-run", false, "检查并打印启动命令，但不执行")
	diagnose := flag.Bool("diagnose", false, "打印 Java/JAR 检测结果")
	showVersion := flag.Bool("version", false, "显示版本")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Java 游戏跨平台启动器\n\n用法: %s [选项] [-- 游戏参数...]\n\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Printf("java-game-launcher %s (%s)\n", version, commit)
		return
	}
	absConfigPath, err := filepath.Abs(*configPath)
	if err == nil {
		*configPath = absConfigPath
	}
	loadPath := *configPath
	legacyLoaded := false
	configExplicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			configExplicit = true
		}
	})
	if !configExplicit {
		if _, statErr := os.Stat(loadPath); os.IsNotExist(statErr) {
			legacy := filepath.Join(filepath.Dir(loadPath), legacyConfigFileName)
			if _, legacyErr := os.Stat(legacy); legacyErr == nil {
				loadPath = legacy
				legacyLoaded = true
			}
		}
	}
	cfg, loadErr := loadConfig(loadPath)
	if len(flag.Args()) > 0 {
		cfg.GameArgs = append(cfg.GameArgs, flag.Args()...)
	}
	if *launch || *dryRun || *diagnose {
		runCLI(cfg, *configPath, *launch, *dryRun, *diagnose, loadErr)
		return
	}
	status := "正在扫描本地 JDK、JAVA_HOME 和 PATH…"
	statusErr := false
	if loadErr != nil {
		status = loadErr.Error() + "；已使用默认配置"
		statusErr = true
	} else if legacyLoaded {
		status = "已读取旧版 Mindustry 配置；保存后将迁移为 " + configFileName
	}
	p := tea.NewProgram(newModel(cfg, *configPath, status, statusErr), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "启动 TUI 失败:", err)
		os.Exit(1)
	}
}

func runCLI(cfg Config, cfgPath string, launch, dryRun, diagnose bool, loadErr error) {
	if loadErr != nil {
		fmt.Fprintln(os.Stderr, loadErr)
		os.Exit(1)
	}
	env := discoverEnvironment(cfg, cfgPath)
	changed := applyAutoSelections(&cfg, cfgPath, env)
	if diagnose {
		printDiagnostics(env)
		if !launch && !dryRun {
			return
		}
	}
	if changed && launch {
		if err := saveConfig(cfgPath, cfg); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	spec, err := prepareLaunch(cfg, cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "无法启动:", err)
		os.Exit(1)
	}
	if dryRun {
		fmt.Println("工作目录:", spec.WorkingDir)
		fmt.Println("启动命令:", formatCommand(spec))
		return
	}
	if launch {
		if err := ensureLaunchDirectories(spec); err != nil {
			fmt.Fprintln(os.Stderr, "无法启动:", err)
			os.Exit(1)
		}
		if err := spec.Command.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "游戏进程异常结束:", err)
			os.Exit(1)
		}
	}
}

func printDiagnostics(env Environment) {
	fmt.Println("Java 检测结果:")
	if len(env.Java) == 0 {
		fmt.Println("  未找到")
	}
	for _, candidate := range env.Java {
		if candidate.Err != nil {
			fmt.Printf("  [不可用] %s (%s, %s): %v\n", candidate.Path, candidate.Source, javaArchitectureLabel(candidate), candidate.Err)
		} else {
			fmt.Printf("  [Java %d, %s] %s (%s, %s)\n", candidate.Version, javaArchitectureLabel(candidate), candidate.Path, candidate.Source, candidate.VersionText)
		}
	}
	fmt.Println("JAR 检测结果:")
	if len(env.Jars) == 0 {
		fmt.Println("  未找到")
	}
	for _, jar := range env.Jars {
		if jar.Err != nil {
			fmt.Printf("  [不可用] %s: %v\n", jar.Path, jar.Err)
		} else {
			native := strings.Join(jar.NativeArchitectures, "/")
			if native == "" {
				native = "未声明"
			}
			fmt.Printf("  [%s, Java %d+, 原生架构 %s] %s (Main-Class: %s)\n", jar.ProfileName, jar.RequiredJavaVersion, native, jar.Path, jar.MainClass)
		}
	}
}

func javaArchitectureLabel(candidate JavaCandidate) string {
	arch := candidate.Architecture
	if arch == "" {
		arch = "未知架构"
	}
	if candidate.DataModel > 0 {
		return fmt.Sprintf("%s/%d位", arch, candidate.DataModel)
	}
	return arch
}
