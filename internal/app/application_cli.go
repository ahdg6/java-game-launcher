package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/ahdg6/java-game-launcher/internal/diagnostics"
)

type cliOptions struct {
	configPath                          string
	instance                            string
	launch, dryRun, diagnose, preflight bool
	gameArgs                            []string
}

func parseCLIOptions(args []string, output io.Writer) (cliOptions, error) {
	var options cliOptions
	flags := flag.NewFlagSet("java-game-launcher", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&options.configPath, "config", defaultConfigPath(), "配置文件路径")
	flags.StringVar(&options.instance, "instance", "", "选择实例（优先匹配 ID，名称必须唯一）")
	flags.BoolVar(&options.launch, "launch", false, "不进入 TUI，直接启动游戏")
	flags.BoolVar(&options.dryRun, "dry-run", false, "检查并打印启动命令，但不执行")
	flags.BoolVar(&options.diagnose, "diagnose", false, "打印 Java/JAR 检测结果")
	flags.BoolVar(&options.preflight, "preflight", false, "执行完整启动前检查（含 JVM 参数试运行）")
	flags.Usage = func() {
		fmt.Fprintln(output, "Mindustry-first Java 游戏启动器\n\n用法: java-game-launcher [选项] [-- 游戏参数...]")
		fmt.Fprintln(output)
		flags.PrintDefaults()
	}
	err := flags.Parse(args)
	options.gameArgs = flags.Args()
	return options, err
}

func runCLI(cfg Config, launcher *LauncherConfig, options cliOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	cfgPath := options.configPath
	env := discoverEnvironment(cfg, cfgPath)
	changed := applyAutoSelections(&cfg, cfgPath, env)
	if options.diagnose {
		printDiagnostics(stdout, env)
		if !options.launch && !options.dryRun && !options.preflight {
			return nil
		}
	}
	if changed && options.launch {
		if err := saveCLIAutoSelections(cfgPath, launcher, cfg); err != nil {
			return err
		}
	}
	cfg.GameArgs = append(append([]string{}, cfg.GameArgs...), options.gameArgs...)
	var spec LaunchSpec
	if options.preflight {
		report := RunLaunchPreflight(cfg, cfgPath)
		printPreflightReport(stdout, report)
		if !report.Ready {
			return errors.New("启动前检查失败")
		}
		if !options.launch && !options.dryRun {
			return nil
		}
		spec = report.Spec
	} else {
		var err error
		spec, err = prepareLaunch(cfg, cfgPath)
		if err != nil {
			return fmt.Errorf("无法启动: %w", err)
		}
	}
	if options.dryRun {
		fmt.Fprintln(stdout, "工作目录:", spec.WorkingDir)
		fmt.Fprintln(stdout, "启动命令:", formatCommand(spec))
		return nil
	}
	if options.launch {
		if err := ensureLaunchDirectories(spec); err != nil {
			return fmt.Errorf("无法启动: %w", err)
		}
		if err := runCLIProcess(spec, cfgPath, stdin, stdout, stderr); err != nil {
			return fmt.Errorf("游戏进程异常结束: %w", err)
		}
	}
	return nil
}

func saveCLIAutoSelections(cfgPath string, launcher *LauncherConfig, cfg Config) error {
	instance := launcher.InstanceByID(cfg.InstanceID)
	if instance == nil {
		return errors.New("保存自动选择：当前实例不存在")
	}
	// --instance selects one invocation. Persist newly discovered paths for that
	// instance without silently changing the TUI's configured active instance.
	instance.ApplyConfig(cfg)
	return saveLauncherConfig(cfgPath, *launcher)
}

func runCLIProcess(spec LaunchSpec, cfgPath string, stdin io.Reader, stdout, stderr io.Writer) error {
	session := newLaunchSession(spec, cfgPath)
	if path := session.writer.logPath(); path != "" {
		fmt.Fprintln(stderr, "[启动器] 持久日志:", path)
	}
	spec.Command.Stdin = stdin
	spec.Command.Stdout = io.MultiWriter(stdout, session.writer)
	spec.Command.Stderr = io.MultiWriter(stderr, session.writer)
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
			fmt.Fprintln(stderr, "[启动器] 完整日志已保存:", logPath)
		}
		for _, diagnostic := range diagnostics.AnalyzeLaunchFailure(normalizeLog(output), err) {
			fmt.Fprintf(stderr, "[诊断] %s：%s\n", diagnostic.Title, diagnostic.Summary)
			for _, suggestion := range diagnostic.Suggestions {
				fmt.Fprintln(stderr, "  -", suggestion)
			}
		}
	}
	return err
}

func printPreflightReport(out io.Writer, report PreflightReport) {
	for _, check := range report.Checks {
		mark := "OK"
		if check.Level == PreflightWarning {
			mark = "WARN"
		} else if check.Level == PreflightError {
			mark = "ERROR"
		}
		fmt.Fprintf(out, "[%s] %s: %s\n", mark, check.Name, check.Summary)
	}
	if report.Ready {
		fmt.Fprintln(out, "启动前检查通过")
	} else {
		fmt.Fprintln(out, "启动前检查失败")
	}
}

func printDiagnostics(out io.Writer, env Environment) {
	fmt.Fprintln(out, "Java 检测结果:")
	if len(env.Java) == 0 {
		fmt.Fprintln(out, "  未找到")
	}
	for _, candidate := range env.Java {
		if candidate.Err != nil {
			fmt.Fprintf(out, "  [不可用] %s (%s, %s): %v\n", candidate.Path, candidate.Source, javaArchitectureLabel(candidate), candidate.Err)
		} else {
			fmt.Fprintf(out, "  [Java %d, %s] %s (%s, %s)\n", candidate.Version, javaArchitectureLabel(candidate), candidate.Path, candidate.Source, candidate.VersionText)
		}
	}
	fmt.Fprintln(out, "JAR 检测结果:")
	if len(env.Jars) == 0 {
		fmt.Fprintln(out, "  未找到")
	}
	for _, jar := range env.Jars {
		if jar.Err != nil {
			fmt.Fprintf(out, "  [不可用] %s: %v\n", jar.Path, jar.Err)
		} else {
			native := strings.Join(jar.NativeArchitectures, "/")
			if native == "" {
				native = "未声明"
			}
			fmt.Fprintf(out, "  [%s, Java %d+, 原生架构 %s] %s (Main-Class: %s)\n", jar.ProfileName, jar.RequiredJavaVersion, native, jar.Path, jar.MainClass)
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
