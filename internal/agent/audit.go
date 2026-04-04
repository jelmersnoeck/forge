package agent

import (
	"log"

	"github.com/jelmersnoeck/forge/internal/tools"
	"github.com/jelmersnoeck/forge/internal/types"
)

// StdAuditLogger logs agent activity to the standard logger.
// Swap this out for a structured/remote logger to enable audit trails.
type StdAuditLogger struct{}

func (l *StdAuditLogger) LogToolCall(e types.ToolCallEvent) {
	summary := tools.CallSummary(e.ToolName, e.Input)
	if e.Error != nil {
		log.Printf("[audit:%s] tool=%s err=%v duration=%s %s",
			e.SessionID, e.ToolName, e.Error, e.Duration, summary)
		return
	}
	log.Printf("[audit:%s] tool=%s duration=%s %s",
		e.SessionID, e.ToolName, e.Duration, summary)
}
