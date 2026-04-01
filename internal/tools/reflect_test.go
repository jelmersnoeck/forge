package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestReflectTool(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		input       map[string]any
		wantErr     bool
		wantContain string
	}{
		"missing summary": {
			input:   map[string]any{},
			wantErr: true,
		},
		"basic reflection": {
			input: map[string]any{
				"summary": "Implemented feature X",
			},
			wantContain: "Session Reflection",
		},
		"full reflection": {
			input: map[string]any{
				"summary":     "Added AGENTS.md support",
				"mistakes":    []any{"Forgot to handle nil case", "Used wrong type"},
				"successes":   []any{"Tests passed", "Code is clean"},
				"suggestions": []any{"Add more tests", "Improve documentation"},
			},
			wantContain: "Mistakes & Improvements",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tmpDir := t.TempDir()
			ctx := types.ToolContext{
				CWD: tmpDir,
			}

			tool := ReflectTool()
			result, err := tool.Handler(tc.input, ctx)

			if tc.wantErr {
				r.True(result.IsError || err != nil)
				return
			}

			r.NoError(err)
			r.False(result.IsError)

			// Check that AGENTS.md was created
			agentsPath := filepath.Join(tmpDir, "AGENTS.md")
			content, err := os.ReadFile(agentsPath)
			r.NoError(err)
			r.Contains(string(content), tc.wantContain)
		})
	}
}

func TestReflectToolAppends(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	ctx := types.ToolContext{
		CWD: tmpDir,
	}

	tool := ReflectTool()

	// First reflection
	input1 := map[string]any{
		"summary": "First session",
	}
	_, err := tool.Handler(input1, ctx)
	r.NoError(err)

	// Second reflection
	input2 := map[string]any{
		"summary": "Second session",
	}
	_, err = tool.Handler(input2, ctx)
	r.NoError(err)

	// Verify both are in the file
	agentsPath := filepath.Join(tmpDir, "AGENTS.md")
	content, err := os.ReadFile(agentsPath)
	r.NoError(err)

	contentStr := string(content)
	r.Contains(contentStr, "First session")
	r.Contains(contentStr, "Second session")

	// Verify "Second session" comes after "First session"
	firstIdx := strings.Index(contentStr, "First session")
	secondIdx := strings.Index(contentStr, "Second session")
	r.True(secondIdx > firstIdx, "Second session should appear after first")
}
