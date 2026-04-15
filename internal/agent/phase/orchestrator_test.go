package phase

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jelmersnoeck/forge/internal/review"
	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestNewSWEOrchestrator(t *testing.T) {
	r := require.New(t)
	orch := NewSWEOrchestrator()
	r.NotNil(orch)
	r.Equal(maxReviewCycles, orch.maxReviewCycles)
}

func TestBuildCoderPrompt(t *testing.T) {
	tests := map[string]struct {
		specPath     string
		wantContains string
	}{
		"no spec": {
			specPath:     "",
			wantContains: "Implement the changes",
		},
		"spec path that doesn't exist": {
			specPath:     "/nonexistent/greendale/paintball-spec.md",
			wantContains: "Read the spec file first",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			prompt := buildCoderPrompt(tc.specPath)
			r.Contains(prompt, tc.wantContains)
		})
	}
}

func TestBuildCoderPromptWithRealFile(t *testing.T) {
	r := require.New(t)

	// Write a spec file.
	dir := t.TempDir()
	specPath := filepath.Join(dir, "test-spec.md")
	content := "# Troy Barnes enrollment spec\n\nHe's a football star at Greendale."
	r.NoError(os.WriteFile(specPath, []byte(content), 0o644))

	prompt := buildCoderPrompt(specPath)
	r.Contains(prompt, "Troy Barnes enrollment spec")
	r.Contains(prompt, "football star at Greendale")
	r.Contains(prompt, "source of truth")
}

func TestFormatFindingsForCoder(t *testing.T) {
	r := require.New(t)

	findings := []review.Finding{
		{
			Severity:    review.SeverityCritical,
			File:        "internal/paintball/gun.go",
			StartLine:   42,
			Description: "SQL injection in query builder",
		},
		{
			Severity:    review.SeverityPraise,
			Description: "Great error handling in Human Being mascot module",
		},
		{
			Severity:    review.SeverityWarning,
			Description: "Missing nil check on Señor Chang's credentials",
		},
	}

	result := formatFindingsForCoder(findings)
	r.Contains(result, "SQL injection")
	r.Contains(result, "internal/paintball/gun.go:42")
	r.Contains(result, "Missing nil check")
	// Praise should be excluded.
	r.NotContains(result, "Great error handling")
}

func TestFormatFindingsForCoderEmpty(t *testing.T) {
	r := require.New(t)
	result := formatFindingsForCoder(nil)
	r.Contains(result, "code review found the following issues")
}

func TestEmitHelpers(t *testing.T) {
	r := require.New(t)
	orch := NewSWEOrchestrator()

	var events []string
	opts := OrchestratorOpts{
		SessionID: "greendale-101",
		Emit: func(e types.OutboundEvent) {
			events = append(events, e.Type+":"+e.Content)
		},
	}

	orch.emitPhaseStart(opts, "spec")
	orch.emitPhaseComplete(opts, "spec", "done")
	orch.emitPhaseHandoff(opts, "spec", "code")

	r.Len(events, 3)
	r.Equal("phase_start:spec", events[0])
	r.Equal("phase_complete:spec: done", events[1])
	r.Contains(events[2], "phase_handoff:")
	r.Contains(events[2], "spec → code")
}

func TestOrchestratorResult(t *testing.T) {
	r := require.New(t)

	// OrchestratorResult with question intent.
	result := OrchestratorResult{
		Intent:      IntentQuestion,
		QAHistoryID: "greendale-history-101",
	}
	r.Equal(IntentQuestion, result.Intent)
	r.Equal("greendale-history-101", result.QAHistoryID)

	// OrchestratorResult with task intent — QAHistoryID should be empty.
	taskResult := OrchestratorResult{
		Intent: IntentTask,
	}
	r.Equal(IntentTask, taskResult.Intent)
	r.Empty(taskResult.QAHistoryID)
}

func TestQAPhaseDisallowedTools(t *testing.T) {
	r := require.New(t)
	qa := QA()

	// Verify all spec-mandated disallowed tools are present.
	wantDisallowed := []string{
		"Write", "Edit", "PRCreate",
		"Agent", "AgentGet", "AgentList", "AgentStop",
		"TaskCreate", "TaskGet", "TaskList", "TaskStop", "TaskOutput",
		"QueueImmediate", "QueueOnComplete",
		"UseMCPTool",
		"Reflect",
	}

	for _, tool := range wantDisallowed {
		r.Contains(qa.DisallowedTools, tool,
			"QA phase should disallow %s", tool)
	}

	// Read-only tools should NOT be disallowed.
	readOnlyTools := []string{"Read", "Grep", "Glob", "Bash", "WebSearch"}
	for _, tool := range readOnlyTools {
		r.NotContains(qa.DisallowedTools, tool,
			"QA phase should allow %s", tool)
	}
}
