package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type PreflightLevel string

const (
	PreflightPass    PreflightLevel = "pass"
	PreflightWarning PreflightLevel = "warning"
	PreflightError   PreflightLevel = "error"
)

type PreflightCheck struct {
	Name    string
	Level   PreflightLevel
	Summary string
}

type PreflightReport struct {
	Spec   LaunchSpec
	Checks []PreflightCheck
	Ready  bool
}

// RunLaunchPreflight exercises the same launch preparation path as a real
// start, then performs a lightweight JVM option trial and write checks. Heap
// sizes are overridden and AlwaysPreTouch is removed for the trial so checking
// a 4 GiB preset does not itself reserve and touch 4 GiB of RAM.
func RunLaunchPreflight(cfg Config, cfgPath string) PreflightReport {
	report := PreflightReport{}
	spec, err := prepareLaunch(cfg, cfgPath)
	if err != nil {
		report.Checks = append(report.Checks, PreflightCheck{
			Name: "启动计划", Level: PreflightError, Summary: err.Error(),
		})
		return report
	}
	report.Spec = spec
	report.Checks = append(report.Checks,
		PreflightCheck{
			Name: "Java 运行时", Level: PreflightPass,
			Summary: fmt.Sprintf("Java %d · %s · %s", spec.Java.Version, javaArchitectureLabel(spec.Java), spec.Java.Path),
		},
		PreflightCheck{
			Name: "游戏 JAR", Level: PreflightPass,
			Summary: fmt.Sprintf("%s · Main-Class %s · 需要 Java %d+", filepath.Base(spec.Jar.Path), spec.Jar.MainClass, spec.Jar.RequiredJavaVersion),
		},
	)
	if pending, pendingErr := IsMindustrySafeModePending(safeModeStateDirectory(cfgPath, cfg.InstanceID)); pendingErr != nil {
		report.Checks = append(report.Checks, PreflightCheck{
			Name: "安全模式恢复", Level: PreflightError, Summary: pendingErr.Error(),
		})
	} else if pending {
		report.Checks = append(report.Checks, PreflightCheck{
			Name: "安全模式恢复", Level: PreflightError,
			Summary: "当前实例仍有待恢复的模组事务；请先确认没有同实例游戏在运行并完成恢复",
		})
	}

	if err := ensureLaunchDirectories(spec); err != nil {
		report.Checks = append(report.Checks, PreflightCheck{Name: "目录", Level: PreflightError, Summary: err.Error()})
	} else {
		paths := []struct{ name, path string }{{"工作目录", spec.WorkingDir}}
		if spec.DataDirectory != "" {
			paths = append(paths, struct{ name, path string }{"数据目录", spec.DataDirectory})
		}
		for _, candidate := range paths {
			if err := checkDirectoryWritable(candidate.path); err != nil {
				report.Checks = append(report.Checks, PreflightCheck{
					Name: candidate.name, Level: PreflightError, Summary: err.Error(),
				})
			} else {
				report.Checks = append(report.Checks, PreflightCheck{
					Name: candidate.name, Level: PreflightPass, Summary: candidate.path + " · 可写",
				})
			}
		}
	}

	if spec.NeedsGraphics && runtime.GOOS == "linux" &&
		strings.TrimSpace(os.Getenv("DISPLAY")) == "" && strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) == "" {
		report.Checks = append(report.Checks, PreflightCheck{
			Name: "图形会话", Level: PreflightWarning,
			Summary: "未检测到 DISPLAY 或 WAYLAND_DISPLAY；从纯终端/SSH 启动桌面版可能失败",
		})
	} else if spec.NeedsGraphics {
		report.Checks = append(report.Checks, PreflightCheck{Name: "图形会话", Level: PreflightPass, Summary: "已检测到可用的桌面图形会话"})
	} else {
		report.Checks = append(report.Checks, PreflightCheck{Name: "运行模式", Level: PreflightPass, Summary: "服务器 JAR 不需要图形会话，并启用交互控制台"})
	}

	if output, err := trialJVMArguments(spec); err != nil {
		summary := err.Error()
		if trimmed := strings.TrimSpace(output); trimmed != "" {
			summary += "：" + clampPreflightOutput(trimmed, 600)
		}
		report.Checks = append(report.Checks, PreflightCheck{Name: "JVM 参数试运行", Level: PreflightError, Summary: summary})
	} else {
		report.Checks = append(report.Checks, PreflightCheck{
			Name: "JVM 参数试运行", Level: PreflightPass,
			Summary: "所有 JVM 参数均被当前 Java 接受（试运行使用 16–64 MiB 临时堆）",
		})
	}

	report.Ready = true
	for _, check := range report.Checks {
		if check.Level == PreflightError {
			report.Ready = false
			break
		}
	}
	return report
}

func checkDirectoryWritable(directory string) error {
	file, err := os.CreateTemp(directory, ".launcher-write-check-*")
	if err != nil {
		return fmt.Errorf("%s 不可写：%w", directory, err)
	}
	name := file.Name()
	if closeErr := file.Close(); closeErr != nil {
		_ = os.Remove(name)
		return fmt.Errorf("%s 写入检查失败：%w", directory, closeErr)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("%s 清理写入检查文件失败：%w", directory, err)
	}
	return nil
}

func trialJVMArguments(spec LaunchSpec) (string, error) {
	jarIndex := -1
	for index, arg := range spec.Args {
		if arg == "-jar" {
			jarIndex = index
			break
		}
	}
	if jarIndex < 0 {
		return "", errors.New("启动参数中缺少 -jar 分隔符")
	}
	args := make([]string, 0, jarIndex+3)
	for _, arg := range spec.Args[:jarIndex] {
		if arg == "-XX:+AlwaysPreTouch" {
			continue
		}
		args = append(args, arg)
	}
	args = append(args, "-Xms16m", "-Xmx64m", "-version")
	contextWithTimeout, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	command := exec.CommandContext(contextWithTimeout, spec.Java.Path, args...)
	command.Dir = spec.WorkingDir
	output, err := command.CombinedOutput()
	if errors.Is(contextWithTimeout.Err(), context.DeadlineExceeded) {
		return string(output), errors.New("JVM 参数试运行超时")
	}
	if err != nil {
		return string(output), fmt.Errorf("JVM 参数被拒绝: %w", err)
	}
	return string(output), nil
}

func clampPreflightOutput(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}
