package agent

import (
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSubAgentCWD(t *testing.T) {
	tests := map[string]struct {
		parentCWD string
		agentCWD  string
		want      string
	}{
		"override set":   {parentCWD: "/repo", agentCWD: "/tmp/forge/worktrees/jelmer-greendale-1", want: "/tmp/forge/worktrees/jelmer-greendale-1"},
		"override empty": {parentCWD: "/repo", agentCWD: "", want: "/repo"},
		"both empty":     {parentCWD: "", agentCWD: "", want: ""},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := subAgentCWD(tc.parentCWD, &types.SubAgent{CWD: tc.agentCWD})
			require.Equal(t, tc.want, got)
		})
	}
}
