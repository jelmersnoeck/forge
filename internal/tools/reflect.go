package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
)

// ReflectTool returns the tool definition for session reflection.
func ReflectTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "Reflect",
		Description: "Reflect on the current session, capturing learnings, mistakes, and successful patterns. This information is automatically appended to AGENTS.md for future self-improvement.",
		ReadOnly:    false,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"summary": map[string]any{
					"type":        "string",
					"description": "Brief summary of what was accomplished in this session",
				},
				"mistakes": map[string]any{
					"type":        "array",
					"description": "List of mistakes made or things that could have been done better",
					"items": map[string]any{
						"type": "string",
					},
				},
				"successes": map[string]any{
					"type":        "array",
					"description": "List of patterns or approaches that worked well",
					"items": map[string]any{
						"type": "string",
					},
				},
				"suggestions": map[string]any{
					"type":        "array",
					"description": "Ideas for future improvement or things to remember",
					"items": map[string]any{
						"type": "string",
					},
				},
			},
			"required": []string{"summary"},
		},
		Handler: executeReflect,
	}
}

func executeReflect(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	summary, _ := input["summary"].(string)
	if summary == "" {
		return types.ToolResult{IsError: true}, fmt.Errorf("summary is required")
	}

	// Extract arrays safely
	mistakes := extractStringArray(input, "mistakes")
	successes := extractStringArray(input, "successes")
	suggestions := extractStringArray(input, "suggestions")

	// Format the reflection entry
	var entry strings.Builder
	fmt.Fprintf(&entry, "\n## Session Reflection - %s\n\n", time.Now().Format("2006-01-02 15:04"))
	fmt.Fprintf(&entry, "**Summary:** %s\n\n", summary)

	if len(mistakes) > 0 {
		entry.WriteString("**Mistakes & Improvements:**\n")
		for _, m := range mistakes {
			fmt.Fprintf(&entry, "- %s\n", m)
		}
		entry.WriteString("\n")
	}

	if len(successes) > 0 {
		entry.WriteString("**Successful Patterns:**\n")
		for _, s := range successes {
			fmt.Fprintf(&entry, "- %s\n", s)
		}
		entry.WriteString("\n")
	}

	if len(suggestions) > 0 {
		entry.WriteString("**Future Suggestions:**\n")
		for _, s := range suggestions {
			fmt.Fprintf(&entry, "- %s\n", s)
		}
		entry.WriteString("\n")
	}

	// Determine AGENTS.md path (prefer project-level)
	agentsPath := filepath.Join(ctx.CWD, "AGENTS.md")

	// Create file if it doesn't exist
	if _, err := os.Stat(agentsPath); os.IsNotExist(err) {
		header := `# Agent Learnings

This file contains self-improvement learnings from agent sessions. The agent automatically reflects on each session and appends insights here.

`
		if err := os.WriteFile(agentsPath, []byte(header), 0644); err != nil {
			return types.ToolResult{IsError: true}, fmt.Errorf("create AGENTS.md: %v", err)
		}
	}

	// Append reflection
	f, err := os.OpenFile(agentsPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return types.ToolResult{IsError: true}, fmt.Errorf("open AGENTS.md: %v", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.WriteString(entry.String()); err != nil {
		return types.ToolResult{IsError: true}, fmt.Errorf("write to AGENTS.md: %v", err)
	}

	return types.ToolResult{
		Content: []types.ToolResultContent{
			{Type: "text", Text: fmt.Sprintf("Reflection saved to %s", agentsPath)},
		},
	}, nil
}

func extractStringArray(input map[string]any, key string) []string {
	val, ok := input[key]
	if !ok {
		return nil
	}

	arr, ok := val.([]any)
	if !ok {
		return nil
	}

	result := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok && s != "" {
			result = append(result, s)
		}
	}
	return result
}
