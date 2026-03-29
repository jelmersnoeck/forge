package tools

import (
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
