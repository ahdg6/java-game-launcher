package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
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
	instanceSelector := flag.String("instance", "", "选择实例（优先匹配 ID，名称必须唯一）")
	launch := flag.Bool("launch", false, "不进入 TUI，直接启动游戏")
	dryRun := flag.Bool("dry-run", false, "检查并打印启动命令，但不执行")
	diagnose := flag.Bool("diagnose", false, "打印 Java/JAR 检测结果")
	preflight := flag.Bool("preflight", false, "执行完整启动前检查（含 JVM 参数试运行）")
	showVersion := flag.Bool("version", false, "显示版本")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Mindustry-first Java 游戏启动器\n\n用法: %s [选项] [-- 游戏参数...]\n\n", filepath.Base(os.Args[0]))
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
	launcherCfg, loadErr := loadLauncherConfig(loadPath)
	cfg := defaultConfig()
	loadWarnings := []string(nil)
	recoveredSafeModes := []string(nil)
	selectedName := ""
	var selectionErr error
	var recoveryErr error
	if loadErr == nil {
		loadWarnings = launcherCfg.Warnings
	}
	if loadErr == nil {
		var selected *InstanceConfig
		if *instanceSelector == "" {
			selected, selectionErr = launcherCfg.Active()
		} else {
			selected, selectionErr = launcherCfg.ResolveInstance(*instanceSelector)
		}
		if selectionErr != nil {
			selected, _ = launcherCfg.Active()
		}
		if selected != nil {
			cfg = selected.Config()
			selectedName = selected.Name
			launcherCfg.ActiveInstanceID = selected.ID
		}
	}
	cliMode := *launch || *dryRun || *diagnose || *preflight
	if loadErr == nil {
		if cliMode && selectionErr == nil {
			var recovered bool
			recovered, recoveryErr = recoverInstanceSafeMode(cfg, *configPath)
			if recovered {
				recoveredSafeModes = append(recoveredSafeModes, selectedName)
			}
		} else if !cliMode {
			recoveredSafeModes, recoveryErr = recoverLauncherSafeModes(launcherCfg, *configPath)
		}
	}
	if cliMode {
		runCLI(cfg, *configPath, *launch, *dryRun, *diagnose, *preflight, errors.Join(loadErr, selectionErr, recoveryErr), flag.Args())
		return
	}
	if len(flag.Args()) > 0 {
		cfg.GameArgs = append(cfg.GameArgs, flag.Args()...)
	}
	if selected := launcherCfg.InstanceByID(cfg.InstanceID); selected != nil {
		selected.ApplyConfig(cfg)
	}
	status := "正在扫描本地 JDK、JAVA_HOME 和 PATH…"
	statusErr := false
	if loadErr != nil {
		status = loadErr.Error() + "；已使用默认配置"
		statusErr = true
	} else if recoveryErr != nil {
		status = "自动恢复上次安全模式失败：" + recoveryErr.Error()
		statusErr = true
	} else if selectionErr != nil {
		status = selectionErr.Error() + "；已回退到配置中的活动实例"
		statusErr = true
	} else if legacyLoaded {
		status = "已读取旧版 Mindustry 配置；保存后将迁移为 " + configFileName
	} else if len(loadWarnings) > 0 {
		status = strings.Join(loadWarnings, "；")
		statusErr = true
	} else if len(recoveredSafeModes) > 0 {
		status = "已恢复上次中断的安全模式模组：" + strings.Join(recoveredSafeModes, "、")
	}
	p := tea.NewProgram(newModel(launcherCfg, *configPath, status, statusErr), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "启动 TUI 失败:", err)
		os.Exit(1)
	}
}

func runCLI(cfg Config, cfgPath string, launch, dryRun, diagnose, preflight bool, loadErr error, extraGameArgs []string) {
	if loadErr != nil {
		fmt.Fprintln(os.Stderr, loadErr)
		os.Exit(1)
	}
	env := discoverEnvironment(cfg, cfgPath)
	changed := applyAutoSelections(&cfg, cfgPath, env)
	if diagnose {
		printDiagnostics(env)
		if !launch && !dryRun && !preflight {
			return
		}
	}
	if changed && launch {
		if err := saveConfig(cfgPath, cfg); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if len(extraGameArgs) > 0 {
		cfg.GameArgs = append(append([]string{}, cfg.GameArgs...), extraGameArgs...)
	}
	if preflight {
		report := RunLaunchPreflight(cfg, cfgPath)
		printPreflightReport(report)
		if !report.Ready {
			os.Exit(1)
		}
		if !launch && !dryRun {
			return
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
		if err := runCLIProcess(spec, cfgPath); err != nil {
			fmt.Fprintln(os.Stderr, "游戏进程异常结束:", err)
			os.Exit(1)
		}
	}
}

func runCLIProcess(spec LaunchSpec, cfgPath string) error {
	session := newLaunchSession(spec, cfgPath)
	if path := session.writer.logPath(); path != "" {
		fmt.Fprintln(os.Stderr, "[启动器] 持久日志:", path)
	}
	spec.Command.Stdout = io.MultiWriter(os.Stdout, session.writer)
	spec.Command.Stderr = io.MultiWriter(os.Stderr, session.writer)
	err := spec.Command.Run()
	if err != nil {
		_, _ = fmt.Fprintf(session.writer, "\n[启动器] 游戏进程异常结束: %v\n", err)
	} else {
		_, _ = io.WriteString(session.writer, "\n[启动器] 游戏进程已退出。\n")
	}
	output := session.writer.output()
	logPath := session.writer.logPath()
	session.writer.close()
	if err != nil {
		if logPath != "" {
			fmt.Fprintln(os.Stderr, "[启动器] 完整日志已保存:", logPath)
		}
		for _, diagnostic := range AnalyzeLaunchFailure(normalizeLog(output), err) {
			fmt.Fprintf(os.Stderr, "[诊断] %s：%s\n", diagnostic.Title, diagnostic.Summary)
			for _, suggestion := range diagnostic.Suggestions {
				fmt.Fprintln(os.Stderr, "  -", suggestion)
			}
		}
	}
	return err
}

func printPreflightReport(report PreflightReport) {
	for _, check := range report.Checks {
		mark := "OK"
		if check.Level == PreflightWarning {
			mark = "WARN"
		} else if check.Level == PreflightError {
			mark = "ERROR"
		}
		fmt.Printf("[%s] %s: %s\n", mark, check.Name, check.Summary)
	}
	if report.Ready {
		fmt.Println("启动前检查通过")
	} else {
		fmt.Println("启动前检查失败")
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
