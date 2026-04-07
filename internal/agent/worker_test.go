package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestBuildReflectionSummary(t *testing.T) {
	tests := map[string]struct {
		history     []types.ChatMessage
		wantEmpty   bool
		wantContain []string
	}{
		"no tools used": {
			history: []types.ChatMessage{
				{Role: "user", Content: []types.ChatContentBlock{
					{Type: "text", Text: "Hello from Greendale"},
				}},
				{Role: "assistant", Content: []types.ChatContentBlock{
					{Type: "text", Text: "Go Human Beings!"},
				}},
			},
			wantEmpty: true,
		},
		"tools used": {
			history: []types.ChatMessage{
				{Role: "user", Content: []types.ChatContentBlock{
					{Type: "text", Text: "Fix the study room table"},
				}},
				{Role: "assistant", Content: []types.ChatContentBlock{
					{Type: "tool_use", Name: "Read"},
					{Type: "tool_use", Name: "Edit"},
				}},
				{Role: "user", Content: []types.ChatContentBlock{
					{Type: "tool_result"},
				}},
				{Role: "assistant", Content: []types.ChatContentBlock{
					{Type: "tool_use", Name: "Bash"},
				}},
			},
			wantContain: []string{"Fix the study room table", "Bash, Edit, Read", "3 calls"},
		},
		"long prompt truncated": {
			history: []types.ChatMessage{
				{Role: "user", Content: []types.ChatContentBlock{
					{Type: "text", Text: "Dean Pelton writes a very long email to Jeff Winger about the upcoming Greendale dance that goes on and on about costumes and themes and decorations and music choices"},
				}},
				{Role: "assistant", Content: []types.ChatContentBlock{
					{Type: "tool_use", Name: "Bash"},
				}},
			},
			wantContain: []string{"...", "Bash", "1 calls"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			result := buildReflectionSummary(tc.history)
			if tc.wantEmpty {
				r.Empty(result)
				return
			}

			for _, s := range tc.wantContain {
				r.Contains(result, s)
			}
		})
	}
}

func TestAutoReflect(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()

	history := []types.ChatMessage{
		{Role: "user", Content: []types.ChatContentBlock{
			{Type: "text", Text: "Troy and Abed in the morning"},
		}},
		{Role: "assistant", Content: []types.ChatContentBlock{
			{Type: "tool_use", Name: "Bash"},
		}},
	}

	cb := autoReflect(tmpDir)
	cb(history)

	entries, err := os.ReadDir(filepath.Join(tmpDir, ".forge", "learnings"))
	r.NoError(err)
	r.Len(entries, 1)

	content, err := os.ReadFile(filepath.Join(tmpDir, ".forge", "learnings", entries[0].Name()))
	r.NoError(err)
	r.Contains(string(content), "Troy and Abed in the morning")
	r.Contains(string(content), "Bash")
}

func TestAutoReflect_NoToolsNoFile(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()

	history := []types.ChatMessage{
		{Role: "user", Content: []types.ChatContentBlock{
			{Type: "text", Text: "Just chatting"},
		}},
		{Role: "assistant", Content: []types.ChatContentBlock{
			{Type: "text", Text: "Cool cool cool"},
		}},
	}

	cb := autoReflect(tmpDir)
	cb(history)

	// No learnings directory should exist — no tools were used.
	_, err := os.Stat(filepath.Join(tmpDir, ".forge", "learnings"))
	r.True(os.IsNotExist(err))
}
