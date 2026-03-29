package agent

import (
	"log"

	"github.com/jelmersnoeck/forge/internal/types"
)

// StdAuditLogger logs agent activity to the standard logger.
// Swap this out for a structured/remote logger to enable audit trails.
type StdAuditLogger struct{}

func (l *StdAuditLogger) LogToolCall(e types.ToolCallEvent) {
	summary := toolCallSummary(e.ToolName, e.Input)
	if e.Error != nil {
		log.Printf("[audit:%s] tool=%s err=%v duration=%s %s",
			e.SessionID, e.ToolName, e.Error, e.Duration, summary)
		return
	}
	log.Printf("[audit:%s] tool=%s duration=%s %s",
		e.SessionID, e.ToolName, e.Duration, summary)
}

// toolCallSummary returns a short human-readable description of the tool input.
func toolCallSummary(name string, input map[string]any) string {
	str := func(key string) string {
		if v, ok := input[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}

	switch name {
	case "Bash":
		cmd := str("command")
		if len(cmd) > 120 {
			return cmd[:120] + "..."
		}
		return cmd
	case "Read":
		return str("file_path")
	case "Write":
		return str("file_path")
	case "Edit":
		return str("file_path")
	case "Glob":
		return str("pattern")
	case "Grep":
		return str("pattern")
	default:
		return ""
	}
}
