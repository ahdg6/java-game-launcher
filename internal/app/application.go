package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Run starts one launcher invocation and returns its process exit status.
// Arguments exclude the program name; streams belong to the caller.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	options, err := parseCLIOptions(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return 2
	}
	absConfigPath, err := filepath.Abs(options.configPath)
	if err == nil {
		options.configPath = absConfigPath
	}
	launcherCfg, loadErr := loadLauncherConfig(options.configPath)
	cliMode := options.launch || options.dryRun || options.diagnose || options.preflight
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
		if options.instance == "" {
			selected, selectionErr = launcherCfg.Active()
		} else {
			selected, selectionErr = launcherCfg.ResolveInstance(options.instance)
		}
		if selectionErr != nil {
			selected, _ = launcherCfg.Active()
		}
		if selected != nil {
			cfg = selected.Config()
			selectedName = selected.Name
			if !cliMode {
				launcherCfg.ActiveInstanceID = selected.ID
			}
		}
	}
	if loadErr == nil {
		if cliMode && selectionErr == nil {
			var recovered bool
			recovered, recoveryErr = recoverInstance(cfg, options.configPath)
			if recovered {
				recoveredSafeModes = append(recoveredSafeModes, selectedName)
			}
		} else if !cliMode {
			recoveredSafeModes, recoveryErr = recoverAllInstances(launcherCfg, options.configPath)
		}
	}
	if cliMode {
		// A recovery failure is still a failed real launch attempt. Diagnostic
		// invocations leave the operational success history untouched.
		if recoveryErr != nil && options.launch && !options.dryRun {
			if err := clearSuccessfulLaunch(options.configPath, cfg.InstanceID); err != nil {
				fmt.Fprintln(stderr, "[启动器] 无法清除自动启动记录:", err)
			}
		}
		if err := errors.Join(loadErr, selectionErr, recoveryErr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if err := runCLI(cfg, &launcherCfg, options, stdin, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
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
	} else if len(loadWarnings) > 0 {
		status = strings.Join(loadWarnings, "；")
		statusErr = true
	} else if len(recoveredSafeModes) > 0 {
		status = "已恢复上次中断的安全模式模组：" + strings.Join(recoveredSafeModes, "、")
	}
	m := newModel(launcherCfg, options.configPath, status, statusErr, options.gameArgs...)
	if options.noAutoLaunch {
		m.cancelAutoLaunch()
	}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithInput(stdin), tea.WithOutput(stdout))
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(stderr, "启动 TUI 失败:", err)
		return 1
	}
	return 0
}
