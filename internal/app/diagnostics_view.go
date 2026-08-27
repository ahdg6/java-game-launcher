package app

import (
	"fmt"
	"strings"

	"github.com/ahdg6/java-game-launcher/internal/diagnostics"
)

func (m model) logDisplayContent() string {
	if !m.showAnalysis || len(m.diagnostics) == 0 {
		return m.logText
	}
	return renderDiagnostics(m.diagnostics) + "\n\n──────── 完整日志 ────────\n\n" + m.logText
}

func renderDiagnostics(findings []diagnostics.Diagnostic) string {
	var builder strings.Builder
	builder.WriteString("启动器智能诊断\n")
	for index, diagnostic := range findings {
		severity := "提示"
		switch diagnostic.Severity {
		case diagnostics.DiagnosticSeverityError:
			severity = "错误"
		case diagnostics.DiagnosticSeverityWarning:
			severity = "警告"
		}
		builder.WriteString(fmt.Sprintf("\n%d. [%s] %s\n", index+1, severity, diagnostic.Title))
		if diagnostic.Summary != "" {
			builder.WriteString("   " + diagnostic.Summary + "\n")
		}
		for _, suggestion := range diagnostic.Suggestions {
			builder.WriteString("   • " + suggestion + "\n")
		}
	}
	return strings.TrimRight(builder.String(), "\n")
}
