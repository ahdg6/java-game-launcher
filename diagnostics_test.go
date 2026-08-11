package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestAnalyzeLaunchFailureRecognizesKnownFailures(t *testing.T) {
	tests := []struct {
		name           string
		output         string
		processErr     error
		wantCode       string
		wantSummary    string
		wantSuggestion string
	}{
		{
			name: "class version too new",
			output: "java.lang.UnsupportedClassVersionError: mindustry/desktop/DesktopLauncher has been compiled by a more recent version of the Java Runtime " +
				"(class file version 61.0), this version of the Java Runtime only recognizes class file versions up to 55.0",
			processErr: errors.New("exit status 1"), wantCode: "java_version_too_old", wantSummary: "Java 17", wantSuggestion: "Java 17",
		},
		{
			name: "mindustry 32 bit target",
			output: "arc.util.ArcRuntimeException: Couldn't load shared library 'libarc.so' for target: Linux, 32-bit\n" +
				"Caused by: arc.util.ArcRuntimeException: Unable to read file for extraction: libarc.so",
			processErr: errors.New("exit status 1"), wantCode: "java_architecture_mismatch", wantSummary: "架构", wantSuggestion: "64 位 Java",
		},
		{
			name:       "wrong elf class",
			output:     "/tmp/libarc64.so: wrong ELF class: ELFCLASS32",
			processErr: errors.New("exit status 1"), wantCode: "java_architecture_mismatch", wantSummary: "架构", wantSuggestion: "amd64",
		},
		{
			name:       "no video device",
			output:     "SDL Error: No available video device",
			processErr: errors.New("exit status 1"), wantCode: "graphics_environment_unavailable", wantSummary: "SDL", wantSuggestion: "DISPLAY",
		},
		{
			name:       "unrecognized VM option",
			output:     "Unrecognized VM option 'UseConcMarkSweepGC'\nError: Could not create the Java Virtual Machine.",
			processErr: errors.New("exit status 1"), wantCode: "unrecognized_jvm_option", wantSummary: "UseConcMarkSweepGC", wantSuggestion: "删除",
		},
		{
			name:       "heap reservation failed",
			output:     "Error occurred during initialization of VM\nCould not reserve enough space for 4194304KB object heap",
			processErr: errors.New("exit status 1"), wantCode: "java_out_of_memory", wantSummary: "预留", wantSuggestion: "-Xms",
		},
		{
			name:       "runtime out of memory",
			output:     "Exception in thread \"main\" java.lang.OutOfMemoryError: Java heap space",
			processErr: errors.New("exit status 1"), wantCode: "java_out_of_memory", wantSummary: "耗尽", wantSuggestion: "模组",
		},
		{
			name:       "missing module",
			output:     "Error occurred during initialization of boot layer\njava.lang.module.FindException: Module java.desktop not found",
			processErr: errors.New("exit status 1"), wantCode: "missing_java_module", wantSummary: "java.desktop", wantSuggestion: "OpenJDK",
		},
		{
			name: "class not found",
			output: "Error: Could not find or load main class mindustry.desktop.DesktopLauncher\n" +
				"Caused by: java.lang.ClassNotFoundException: mindustry.desktop.DesktopLauncher",
			processErr: errors.New("exit status 1"), wantCode: "missing_java_class", wantSummary: "mindustry.desktop.DesktopLauncher", wantSuggestion: "游戏 JAR",
		},
		{
			name:       "no class definition",
			output:     "java.lang.NoClassDefFoundError: org/lwjgl/Version",
			processErr: errors.New("exit status 1"), wantCode: "missing_java_class", wantSummary: "org.lwjgl.Version", wantSuggestion: "依赖",
		},
		{
			name:       "data directory permission",
			output:     "[启动器] 启动前检查失败: 创建数据目录 /opt/game/game_data: mkdir /opt/game: permission denied",
			processErr: errors.New("permission denied"), wantCode: "filesystem_permission_denied", wantSummary: "数据目录", wantSuggestion: "可读写",
		},
		{
			name:       "native library missing",
			output:     "java.lang.UnsatisfiedLinkError: no lwjgl in java.library.path: /usr/lib",
			processErr: errors.New("exit status 1"), wantCode: "native_library_load_failed", wantSummary: "原生库", wantSuggestion: "完整游戏包",
		},
		{
			name:       "generic non-zero exit",
			output:     "The game stopped without a recognizable stack trace.",
			processErr: errors.New("exit status 7"), wantCode: "process_nonzero_exit", wantSummary: "exit status 7", wantSuggestion: "完整日志",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diagnostics := AnalyzeLaunchFailure(test.output, test.processErr)
			if len(diagnostics) != 1 {
				t.Fatalf("AnalyzeLaunchFailure() returned %d diagnostics, want 1: %#v", len(diagnostics), diagnostics)
			}
			diagnostic := diagnostics[0]
			if diagnostic.Code != test.wantCode {
				t.Fatalf("Code = %q, want %q", diagnostic.Code, test.wantCode)
			}
			if diagnostic.Severity != DiagnosticSeverityError {
				t.Errorf("Severity = %q, want %q", diagnostic.Severity, DiagnosticSeverityError)
			}
			if !strings.Contains(diagnostic.Summary, test.wantSummary) {
				t.Errorf("Summary = %q, want it to contain %q", diagnostic.Summary, test.wantSummary)
			}
			joinedSuggestions := strings.Join(diagnostic.Suggestions, "\n")
			if !strings.Contains(joinedSuggestions, test.wantSuggestion) {
				t.Errorf("Suggestions = %q, want one to contain %q", joinedSuggestions, test.wantSuggestion)
			}
			if diagnostic.Title == "" || diagnostic.Summary == "" || len(diagnostic.Suggestions) == 0 {
				t.Errorf("diagnostic should contain complete user-facing text: %#v", diagnostic)
			}
		})
	}
}

func TestAnalyzeLaunchFailureDeduplicatesRootCauses(t *testing.T) {
	tests := []struct {
		name      string
		output    string
		wantCodes []string
	}{
		{
			name: "repeated out of memory",
			output: "java.lang.OutOfMemoryError: Java heap space\n" +
				"Caused by: java.lang.OutOfMemoryError: Java heap space",
			wantCodes: []string{"java_out_of_memory"},
		},
		{
			name: "architecture suppresses generic native error",
			output: "Couldn't load shared library 'libarc.so' for target: Linux, 32-bit\n" +
				"Unable to read file for extraction: libarc.so\njava.lang.UnsatisfiedLinkError: libarc.so",
			wantCodes: []string{"java_architecture_mismatch"},
		},
		{
			name: "module suppresses secondary missing class",
			output: "java.lang.module.FindException: Module java.desktop not found\n" +
				"java.lang.NoClassDefFoundError: java/awt/Desktop",
			wantCodes: []string{"missing_java_module"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diagnostics := AnalyzeLaunchFailure(test.output, errors.New("exit status 1"))
			codes := diagnosticCodes(diagnostics)
			if !reflect.DeepEqual(codes, test.wantCodes) {
				t.Fatalf("codes = %#v, want %#v", codes, test.wantCodes)
			}
		})
	}
}

func TestAnalyzeLaunchFailureReturnsMultipleIndependentDiagnosticsInStableOrder(t *testing.T) {
	output := strings.Join([]string{
		"SDL Error: No available video device",
		"java.nio.file.AccessDeniedException: /opt/game/game_data",
		"Unrecognized VM option 'OldOption'",
	}, "\n")

	diagnostics := AnalyzeLaunchFailure(output, errors.New("exit status 1"))
	want := []string{
		"unrecognized_jvm_option",
		"graphics_environment_unavailable",
		"filesystem_permission_denied",
	}
	if got := diagnosticCodes(diagnostics); !reflect.DeepEqual(got, want) {
		t.Fatalf("codes = %#v, want %#v", got, want)
	}
	for i := 1; i < len(diagnostics); i++ {
		if severityRank(diagnostics[i-1].Severity) > severityRank(diagnostics[i].Severity) {
			t.Fatalf("diagnostics are not ordered by severity: %#v", diagnostics)
		}
	}
}

func TestAnalyzeLaunchFailureUsesProcessErrorAsInput(t *testing.T) {
	diagnostics := AnalyzeLaunchFailure("", errors.New("fork/exec java: permission denied"))
	if got := diagnosticCodes(diagnostics); !reflect.DeepEqual(got, []string{"filesystem_permission_denied"}) {
		t.Fatalf("codes = %#v, want permission diagnostic", got)
	}
}

func TestAnalyzeLaunchFailureNoFailureEvidence(t *testing.T) {
	if got := AnalyzeLaunchFailure("game exited normally", nil); len(got) != 0 {
		t.Fatalf("AnalyzeLaunchFailure() = %#v, want no diagnostics", got)
	}
}

func TestAnalyzeLaunchFailureDoesNotMisdiagnoseClassInitialization(t *testing.T) {
	tests := []string{
		"java.lang.NoClassDefFoundError: Could not initialize class org.lwjgl.opengl.GL",
		"Property settings:\n    java.library.path = /usr/lib/jni",
	}
	for _, output := range tests {
		diagnostics := AnalyzeLaunchFailure(output, nil)
		if got := diagnosticCodes(diagnostics); len(got) != 0 {
			t.Fatalf("AnalyzeLaunchFailure(%q) codes = %#v, want no diagnosis", output, got)
		}
	}
}

func TestJavaVersionForClassMajor(t *testing.T) {
	tests := []struct {
		major int
		want  int
	}{
		{44, 0},
		{45, 1},
		{52, 8},
		{55, 11},
		{61, 17},
		{65, 21},
		{69, 25},
	}
	for _, test := range tests {
		if got := javaVersionForClassMajor(test.major); got != test.want {
			t.Errorf("javaVersionForClassMajor(%d) = %d, want %d", test.major, got, test.want)
		}
	}
}

func diagnosticCodes(diagnostics []Diagnostic) []string {
	codes := make([]string, len(diagnostics))
	for i, diagnostic := range diagnostics {
		codes[i] = diagnostic.Code
	}
	return codes
}
