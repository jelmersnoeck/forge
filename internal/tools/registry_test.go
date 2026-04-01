package tools

import (
	"strings"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestRegistry(t *testing.T) {
	tests := map[string]struct {
		setup func(*Registry)
		check func(*testing.T, *Registry)
	}{
		"empty registry": {
			setup: func(r *Registry) {},
			check: func(t *testing.T, reg *Registry) {
				r := require.New(t)
				r.Len(reg.All(), 0)
				r.Len(reg.Schemas(), 0)
			},
		},
		"register and get tool": {
			setup: func(reg *Registry) {
				reg.Register(types.ToolDefinition{
					Name:        "troy_tool",
					Description: "Tool for Troy Barnes",
					InputSchema: map[string]any{"type": "object"},
					Handler: func(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
						return types.ToolResult{
							Content: []types.ToolResultContent{{Type: "text", Text: "Cool cool cool"}},
						}, nil
					},
				})
			},
			check: func(t *testing.T, reg *Registry) {
				r := require.New(t)
				def, ok := reg.Get("troy_tool")
				r.True(ok)
				r.Equal("troy_tool", def.Name)
				r.Equal("Tool for Troy Barnes", def.Description)

				_, notFound := reg.Get("abed_tool")
				r.False(notFound)
			},
		},
		"all and schemas": {
			setup: func(reg *Registry) {
				reg.Register(types.ToolDefinition{
					Name:        "greendale_tool",
					Description: "Human Being mascot approved",
					InputSchema: map[string]any{"type": "object"},
					Handler:     func(map[string]any, types.ToolContext) (types.ToolResult, error) { return types.ToolResult{}, nil },
				})
				reg.Register(types.ToolDefinition{
					Name:        "chang_tool",
					Description: "Señor Chang's Spanish class",
					InputSchema: map[string]any{"type": "object"},
					Handler:     func(map[string]any, types.ToolContext) (types.ToolResult, error) { return types.ToolResult{}, nil },
				})
			},
			check: func(t *testing.T, reg *Registry) {
				r := require.New(t)
				all := reg.All()
				r.Len(all, 2)

				schemas := reg.Schemas()
				r.Len(schemas, 2)
				names := make(map[string]bool)
				for _, s := range schemas {
					names[s.Name] = true
				}
				r.True(names["greendale_tool"])
				r.True(names["chang_tool"])
			},
		},
		"execute tool": {
			setup: func(reg *Registry) {
				reg.Register(types.ToolDefinition{
					Name:        "echo_tool",
					Description: "Echoes the input",
					InputSchema: map[string]any{"type": "object"},
					Handler: func(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
						msg := input["message"].(string)
						return types.ToolResult{
							Content: []types.ToolResultContent{{Type: "text", Text: msg}},
						}, nil
					},
				})
			},
			check: func(t *testing.T, reg *Registry) {
				r := require.New(t)
				result, err := reg.Execute("echo_tool", map[string]any{"message": "Six seasons and a movie"}, types.ToolContext{})
				r.NoError(err)
				r.Len(result.Content, 1)
				r.Equal("Six seasons and a movie", result.Content[0].Text)
			},
		},
		"execute missing tool": {
			setup: func(reg *Registry) {},
			check: func(t *testing.T, reg *Registry) {
				r := require.New(t)
				_, err := reg.Execute("missing_tool", map[string]any{}, types.ToolContext{})
				r.Error(err)
				r.Contains(err.Error(), "tool not found")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			reg := NewRegistry()
			tc.setup(reg)
			tc.check(t, reg)
		})
	}
}

func TestNewDefaultRegistry(t *testing.T) {
	r := require.New(t)
	reg := NewDefaultRegistry()

	expectedTools := []string{"Read", "Write", "Edit", "Bash", "Glob", "Grep"}
	for _, name := range expectedTools {
		_, ok := reg.Get(name)
		r.True(ok, "expected tool %s to be registered", name)
	}
}

func TestIsReadOnly(t *testing.T) {
	tests := map[string]struct {
		tools    []types.ToolDefinition
		query    string
		wantRead bool
	}{
		"read-only tool": {
			tools: []types.ToolDefinition{
				{Name: "Glob", ReadOnly: true, Handler: func(map[string]any, types.ToolContext) (types.ToolResult, error) { return types.ToolResult{}, nil }},
			},
			query:    "Glob",
			wantRead: true,
		},
		"mutating tool": {
			tools: []types.ToolDefinition{
				{Name: "Bash", ReadOnly: false, Handler: func(map[string]any, types.ToolContext) (types.ToolResult, error) { return types.ToolResult{}, nil }},
			},
			query:    "Bash",
			wantRead: false,
		},
		"unknown tool": {
			tools:    nil,
			query:    "DeanPeltonTool",
			wantRead: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			reg := NewRegistry()
			for _, tool := range tc.tools {
				reg.Register(tool)
			}
			r.Equal(tc.wantRead, reg.IsReadOnly(tc.query))
		})
	}
}

func TestTruncateResult(t *testing.T) {
	tests := map[string]struct {
		maxChars   int
		inputLen   int
		wantTrunc  bool
		isError    bool
	}{
		"under limit": {
			maxChars:  1000,
			inputLen:  500,
			wantTrunc: false,
		},
		"at limit": {
			maxChars:  1000,
			inputLen:  1000,
			wantTrunc: false,
		},
		"over limit": {
			maxChars:  1000,
			inputLen:  5000,
			wantTrunc: true,
		},
		"error results not truncated": {
			maxChars:  100,
			inputLen:  500,
			wantTrunc: false,
			isError:   true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			reg := NewRegistry()
			reg.maxResultChars = tc.maxChars

			text := strings.Repeat("E Pluribus Anus ", tc.inputLen/16+1)
			text = text[:tc.inputLen]

			reg.Register(types.ToolDefinition{
				Name: "test_tool",
				Handler: func(map[string]any, types.ToolContext) (types.ToolResult, error) {
					return types.ToolResult{
						Content: []types.ToolResultContent{{Type: "text", Text: text}},
						IsError: tc.isError,
					}, nil
				},
			})

			result, err := reg.Execute("test_tool", map[string]any{}, types.ToolContext{})
			r.NoError(err)

			switch {
			case tc.wantTrunc:
				r.Less(len(result.Content[0].Text), tc.inputLen)
				r.Contains(result.Content[0].Text, "truncated")
				// Head and tail should be present.
				r.True(strings.HasPrefix(result.Content[0].Text, text[:100]))
				r.True(strings.HasSuffix(result.Content[0].Text, text[len(text)-100:]))
			default:
				r.Equal(text, result.Content[0].Text)
			}
		})
	}
}

func TestTruncateResult_ImagePassthrough(t *testing.T) {
	r := require.New(t)

	reg := NewRegistry()
	reg.maxResultChars = 10 // very small limit

	reg.Register(types.ToolDefinition{
		Name: "img_tool",
		Handler: func(map[string]any, types.ToolContext) (types.ToolResult, error) {
			return types.ToolResult{
				Content: []types.ToolResultContent{{
					Type:   "image",
					Source: &types.ImageSource{Type: "base64", MediaType: "image/png", Data: strings.Repeat("A", 10000)},
				}},
			}, nil
		},
	})

	result, err := reg.Execute("img_tool", map[string]any{}, types.ToolContext{})
	r.NoError(err)
	r.Equal("image", result.Content[0].Type)
	r.Equal(10000, len(result.Content[0].Source.Data)) // untouched
}
