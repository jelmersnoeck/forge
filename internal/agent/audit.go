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
	keys := map[string]string{
		"Bash": "command", "Read": "file_path", "Write": "file_path",
		"Edit": "file_path", "Glob": "pattern", "Grep": "pattern",
	}
	key, ok := keys[name]
	if !ok {
		return ""
	}
	s, _ := input[key].(string)
	if name == "Bash" && len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}
