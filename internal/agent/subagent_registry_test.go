package agent

import (
	"testing"

	"github.com/jelmersnoeck/forge/internal/tools"
	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestResolveSubAgentRegistry(t *testing.T) {
	nop := func(map[string]any, types.ToolContext) (types.ToolResult, error) {
		return types.ToolResult{}, nil
	}
	parent := func() *tools.Registry {
		reg := tools.NewRegistry()
		for _, name := range []string{"Read", "Write", "Edit", "Bash", "Glob", "WebSearch", "Grep", "Agent"} {
			reg.Register(types.ToolDefinition{Name: name, Handler: nop})
		}
		return reg
	}

	tests := map[string]struct {
		agent       *types.SubAgent
		wantVisible map[string]bool // tool -> in schema
		denyCheck   string          // tool expected to be enforced-denied (empty = skip)
	}{
		"explicit tools win over role": {
			agent: &types.SubAgent{Type: "reviewer", Tools: []string{"Read", "Bash"}},
			// reviewer would deny Bash, but explicit Tools override the preset
			wantVisible: map[string]bool{"Read": true, "Bash": true, "Write": false},
		},
		"explicit disallowed wins over role": {
			agent:       &types.SubAgent{Type: "coder", DisallowedTools: []string{"Bash"}},
			wantVisible: map[string]bool{"Read": true, "Bash": false},
		},
		"reviewer role applied and enforced": {
			agent: &types.SubAgent{Type: "reviewer"},
			wantVisible: map[string]bool{
				"Read": true, "Glob": true, "Grep": true, "WebSearch": true,
				"Write": false, "Edit": false, "Bash": false,
			},
			denyCheck: "Write",
		},
		"coder role is yolo": {
			agent: &types.SubAgent{Type: "coder"},
			wantVisible: map[string]bool{
				"Read": true, "Write": true, "Edit": true, "Bash": true,
			},
		},
		"unknown role unrestricted": {
			agent: &types.SubAgent{Type: "senor_chang"},
			wantVisible: map[string]bool{
				"Read": true, "Write": true, "Bash": true,
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			reg := resolveSubAgentRegistry(parent(), tc.agent)

			visible := make(map[string]bool)
			for _, d := range reg.All() {
				visible[d.Name] = true
			}
			for tool, want := range tc.wantVisible {
				r.Equal(want, visible[tool], "visibility of %s", tool)
			}

			if tc.denyCheck != "" {
				// Re-register to bypass schema hiding, then confirm Execute denies it.
				reg.Register(types.ToolDefinition{Name: tc.denyCheck, Handler: nop})
				res, err := reg.Execute(tc.denyCheck, map[string]any{}, types.ToolContext{})
				r.NoError(err)
				r.True(res.IsError, "%s should be enforced-denied", tc.denyCheck)
			}
		})
	}
}
