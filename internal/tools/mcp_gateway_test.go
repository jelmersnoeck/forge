package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/jelmersnoeck/forge/internal/mcp"
	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestUseMCPTool(t *testing.T) {
	// Helper to build a populated store for tests.
	makeStore := func() *mcp.Store {
		s := mcp.NewStore()
		s.Add(mcp.NewClient("greendale", "http://localhost:1234"), []mcp.MCPTool{
			{
				Name:        "enroll_student",
				Description: "Enroll a student at Greendale Community College. Requires name and major.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":  map[string]any{"type": "string", "description": "Student name"},
						"major": map[string]any{"type": "string", "description": "Declared major"},
					},
					"required": []any{"name"},
				},
			},
			{
				Name:        "expel_student",
				Description: "Expel a student. Only Señor Chang has this power (self-appointed).",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"student_id": map[string]any{"type": "string"},
					},
				},
			},
		})
		return s
	}

	toolCtx := types.ToolContext{
		Ctx: context.Background(),
		CWD: "/tmp",
	}

	tests := map[string]struct {
		store *mcp.Store
		input map[string]any
		check func(*require.Assertions, types.ToolResult)
	}{
		"nil store returns no servers": {
			store: nil,
			input: map[string]any{"action": "list_servers"},
			check: func(r *require.Assertions, result types.ToolResult) {
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "No MCP servers")
			},
		},
		"empty store returns no servers": {
			store: mcp.NewStore(),
			input: map[string]any{"action": "list_servers"},
			check: func(r *require.Assertions, result types.ToolResult) {
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "No MCP servers")
			},
		},
		"list_servers shows connected servers": {
			store: makeStore(),
			input: map[string]any{"action": "list_servers"},
			check: func(r *require.Assertions, result types.ToolResult) {
				r.False(result.IsError)
				r.Contains(result.Content[0].Text, "greendale")
				r.Contains(result.Content[0].Text, "2 tools")
			},
		},
		"list_tools shows tool summaries with schemas": {
			store: makeStore(),
			input: map[string]any{"action": "list_tools", "server": "greendale"},
			check: func(r *require.Assertions, result types.ToolResult) {
				r.False(result.IsError)
				text := result.Content[0].Text
				r.Contains(text, "enroll_student")
				r.Contains(text, "expel_student")
				// Should have truncated description to first sentence
				r.Contains(text, "Enroll a student at Greendale Community College.")
				r.NotContains(text, "Requires name and major")
				// Should include schema
				r.Contains(text, "input_schema")
				r.Contains(text, "name")
			},
		},
		"list_tools requires server": {
			store: makeStore(),
			input: map[string]any{"action": "list_tools"},
			check: func(r *require.Assertions, result types.ToolResult) {
				r.True(result.IsError)
				r.Contains(result.Content[0].Text, "server")
			},
		},
		"list_tools unknown server": {
			store: makeStore(),
			input: map[string]any{"action": "list_tools", "server": "city_college"},
			check: func(r *require.Assertions, result types.ToolResult) {
				r.True(result.IsError)
				r.Contains(result.Content[0].Text, "unknown MCP server")
			},
		},
		"call requires server": {
			store: makeStore(),
			input: map[string]any{"action": "call", "tool": "enroll_student"},
			check: func(r *require.Assertions, result types.ToolResult) {
				r.True(result.IsError)
				r.Contains(result.Content[0].Text, "server")
			},
		},
		"call requires tool": {
			store: makeStore(),
			input: map[string]any{"action": "call", "server": "greendale"},
			check: func(r *require.Assertions, result types.ToolResult) {
				r.True(result.IsError)
				r.Contains(result.Content[0].Text, "tool")
			},
		},
		"unknown action": {
			store: makeStore(),
			input: map[string]any{"action": "paintball"},
			check: func(r *require.Assertions, result types.ToolResult) {
				r.True(result.IsError)
				r.Contains(result.Content[0].Text, "paintball")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			// Set the store for this test
			mcpGatewayStore = tc.store

			def := UseMCPTool()
			result, err := def.Handler(tc.input, toolCtx)
			r.NoError(err)
			tc.check(r, result)
		})
	}
}

func TestFirstSentence(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
	}{
		"single sentence with trailing period": {
			input: "Search raw log entries.",
			want:  "Search raw log entries.",
		},
		"two sentences": {
			input: "Search raw log entries. Do NOT use for counts.",
			want:  "Search raw log entries.",
		},
		"no period": {
			input: "Search raw log entries",
			want:  "Search raw log entries",
		},
		"long text truncated": {
			input: strings.Repeat("x", 300),
			want:  strings.Repeat("x", 200) + "...",
		},
		"empty": {
			input: "",
			want:  "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, firstSentence(tc.input))
		})
	}
}
