package context

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoader_LoadAgentsMD(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		setupFunc func(string) error
		sources   []string
		wantCount int
		wantLevel string
	}{
		"project AGENTS.md": {
			setupFunc: func(dir string) error {
				content := `# Agent Learnings

## Session Reflection - 2024-01-01

**Summary:** Learned how to handle errors better

**Mistakes & Improvements:**
- Forgot to check nil pointers
- Used panic instead of returning error

**Successful Patterns:**
- Early returns work great
- Table-driven tests are clean
`
				return os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0644)
			},
			sources:   []string{"project"},
			wantCount: 1,
			wantLevel: "project",
		},
		"project AGENTS.md in .claude dir": {
			setupFunc: func(dir string) error {
				claudeDir := filepath.Join(dir, ".claude")
				if err := os.MkdirAll(claudeDir, 0755); err != nil {
					return err
				}
				content := `# Agent Learnings from .claude dir`
				return os.WriteFile(filepath.Join(claudeDir, "AGENTS.md"), []byte(content), 0644)
			},
			sources:   []string{"project"},
			wantCount: 1,
			wantLevel: "project",
		},
		"local AGENTS.local.md": {
			setupFunc: func(dir string) error {
				content := `# Local Agent Learnings

Only for this specific directory.
`
				return os.WriteFile(filepath.Join(dir, "AGENTS.local.md"), []byte(content), 0644)
			},
			sources:   []string{"local"},
			wantCount: 1,
			wantLevel: "local",
		},
		"both CLAUDE.md and AGENTS.md": {
			setupFunc: func(dir string) error {
				if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# Project instructions"), 0644); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Agent learnings"), 0644)
			},
			sources:   []string{"project"},
			wantCount: 1,
			wantLevel: "project",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tmpDir := t.TempDir()

			err := tc.setupFunc(tmpDir)
			r.NoError(err)

			loader := NewLoader(tmpDir)
			bundle, err := loader.Load(tc.sources)
			r.NoError(err)

			r.Len(bundle.AgentsMD, tc.wantCount)
			if tc.wantCount > 0 {
				r.Equal(tc.wantLevel, bundle.AgentsMD[0].Level)
			}
		})
	}
}

func TestLoader_LoadMultipleAgentsMD(t *testing.T) {
	r := require.New(t)

	// Create parent directory structure
	tmpRoot := t.TempDir()
	parentDir := filepath.Join(tmpRoot, "parent")
	projectDir := filepath.Join(parentDir, "project")

	err := os.MkdirAll(projectDir, 0755)
	r.NoError(err)

	// Parent AGENTS.md
	parentAgents := `# Parent Agent Learnings

These apply to all child projects.
`
	err = os.WriteFile(filepath.Join(parentDir, "AGENTS.md"), []byte(parentAgents), 0644)
	r.NoError(err)

	// Project AGENTS.md
	projectAgents := `# Project Agent Learnings

Project-specific learnings.
`
	err = os.WriteFile(filepath.Join(projectDir, "AGENTS.md"), []byte(projectAgents), 0644)
	r.NoError(err)

	// Local AGENTS.local.md
	localAgents := `# Local Agent Learnings

Just for this session.
`
	err = os.WriteFile(filepath.Join(projectDir, "AGENTS.local.md"), []byte(localAgents), 0644)
	r.NoError(err)

	loader := NewLoader(projectDir)
	bundle, err := loader.Load([]string{"project", "local"})
	r.NoError(err)

	// Should have parent, project, and local
	r.GreaterOrEqual(len(bundle.AgentsMD), 2, "Should have at least project and local")

	// Verify content
	var foundParent, foundProject, foundLocal bool
	for _, entry := range bundle.AgentsMD {
		switch entry.Level {
		case "parent":
			foundParent = true
			r.Contains(entry.Content, "Parent Agent Learnings")
		case "project":
			foundProject = true
			r.Contains(entry.Content, "Project Agent Learnings")
		case "local":
			foundLocal = true
			r.Contains(entry.Content, "Local Agent Learnings")
		}
	}

	r.True(foundParent, "Should find parent AGENTS.md")
	r.True(foundProject, "Should find project AGENTS.md")
	r.True(foundLocal, "Should find local AGENTS.local.md")
}
