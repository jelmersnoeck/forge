package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveRolePermissions(t *testing.T) {
	tests := map[string]struct {
		role      string
		wantOK    bool
		wantAllow []string
		wantDeny  []string
	}{
		"reviewer": {
			role: "reviewer", wantOK: true,
			wantAllow: []string{"Read", "Glob", "Grep", "WebSearch"},
			wantDeny:  []string{"Write", "Edit", "Bash"},
		},
		"explorer": {
			role: "explorer", wantOK: true,
			wantAllow: []string{"Read", "Glob", "Grep"},
			wantDeny:  []string{"Write", "Edit", "Bash", "Agent"},
		},
		"coder is yolo": {
			role: "coder", wantOK: true,
			wantAllow: []string{"*"}, wantDeny: nil,
		},
		"planner": {
			role: "planner", wantOK: true,
			wantAllow: []string{"Read", "Glob", "Grep", "WebSearch", "Bash"},
			wantDeny:  []string{"Write", "Edit"},
		},
		"case insensitive": {
			role: "Reviewer", wantOK: true,
			wantAllow: []string{"Read", "Glob", "Grep", "WebSearch"},
			wantDeny:  []string{"Write", "Edit", "Bash"},
		},
		"whitespace trimmed": {
			role: "  coder  ", wantOK: true,
			wantAllow: []string{"*"}, wantDeny: nil,
		},
		"unknown role": {
			role: "senor_chang", wantOK: false,
		},
		"empty role": {
			role: "", wantOK: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			cfg, ok := ResolveRolePermissions(tc.role)
			r.Equal(tc.wantOK, ok)
			if tc.wantOK {
				r.Equal(tc.wantAllow, cfg.Allow)
				r.Equal(tc.wantDeny, cfg.Deny)
			}
		})
	}
}

func TestSupportedRoles(t *testing.T) {
	r := require.New(t)
	r.Equal([]string{"coder", "explorer", "planner", "reviewer"}, SupportedRoles())
}
