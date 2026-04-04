package context

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoader_LoadProjectContext(t *testing.T) {
	r := require.New(t)

	projectDir := t.TempDir()

	// Write AGENTS.md to project root
	agentsMD := `# Greendale Community College

This is the project instructions for the study group.
`
	err := os.WriteFile(filepath.Join(projectDir, "AGENTS.md"), []byte(agentsMD), 0644)
	r.NoError(err)

	loader := NewLoader(projectDir)
	bundle, err := loader.Load([]string{"project"})
	r.NoError(err)

	r.Len(bundle.AgentsMD, 1)
	r.Equal("project", bundle.AgentsMD[0].Level)
	r.Contains(bundle.AgentsMD[0].Content, "Greendale Community College")
}

func TestLoader_LoadProjectContext_ForgeDir(t *testing.T) {
	r := require.New(t)

	projectDir := t.TempDir()

	// Write AGENTS.md to .forge/ directory
	forgeDir := filepath.Join(projectDir, ".forge")
	err := os.MkdirAll(forgeDir, 0755)
	r.NoError(err)

	agentsMD := `# Troy and Abed in the morning!`
	err = os.WriteFile(filepath.Join(forgeDir, "AGENTS.md"), []byte(agentsMD), 0644)
	r.NoError(err)

	loader := NewLoader(projectDir)
	bundle, err := loader.Load([]string{"project"})
	r.NoError(err)

	r.Len(bundle.AgentsMD, 1)
	r.Contains(bundle.AgentsMD[0].Content, "Troy and Abed")
}

func TestLoader_LoadProjectContext_ClaudeDirFallback(t *testing.T) {
	r := require.New(t)

	projectDir := t.TempDir()

	// Write AGENTS.md to .claude/ directory (backward compat fallback)
	claudeDir := filepath.Join(projectDir, ".claude")
	err := os.MkdirAll(claudeDir, 0755)
	r.NoError(err)

	agentsMD := `# Streets ahead`
	err = os.WriteFile(filepath.Join(claudeDir, "AGENTS.md"), []byte(agentsMD), 0644)
	r.NoError(err)

	loader := NewLoader(projectDir)
	bundle, err := loader.Load([]string{"project"})
	r.NoError(err)

	r.Len(bundle.AgentsMD, 1)
	r.Contains(bundle.AgentsMD[0].Content, "Streets ahead")
}

func TestLoader_LoadRules(t *testing.T) {
	r := require.New(t)

	projectDir := t.TempDir()
	rulesDir := filepath.Join(projectDir, ".forge", "rules")
	err := os.MkdirAll(rulesDir, 0755)
	r.NoError(err)

	rule1 := `# No paintball

Paintball is banned on campus after the incidents.
`
	err = os.WriteFile(filepath.Join(rulesDir, "paintball.md"), []byte(rule1), 0644)
	r.NoError(err)

	rule2 := `# Study group rules

1. No Troy and Abed in the morning during exams
2. Pierce must attend sensitivity training
`
	err = os.WriteFile(filepath.Join(rulesDir, "study-group.md"), []byte(rule2), 0644)
	r.NoError(err)

	loader := NewLoader(projectDir)
	bundle, err := loader.Load([]string{"project"})
	r.NoError(err)

	r.Len(bundle.Rules, 2)
	r.Equal("project", bundle.Rules[0].Level)
	r.Equal("project", bundle.Rules[1].Level)
}

func TestLoader_LoadRules_ClaudeDirFallback(t *testing.T) {
	r := require.New(t)

	projectDir := t.TempDir()
	rulesDir := filepath.Join(projectDir, ".claude", "rules")
	err := os.MkdirAll(rulesDir, 0755)
	r.NoError(err)

	rule := `# Old rule from .claude dir`
	err = os.WriteFile(filepath.Join(rulesDir, "old.md"), []byte(rule), 0644)
	r.NoError(err)

	loader := NewLoader(projectDir)
	bundle, err := loader.Load([]string{"project"})
	r.NoError(err)

	r.Len(bundle.Rules, 1)
	r.Contains(bundle.Rules[0].Content, "Old rule from .claude dir")
}

func TestLoader_DiscoverSkills(t *testing.T) {
	r := require.New(t)

	projectDir := t.TempDir()
	skillsDir := filepath.Join(projectDir, ".forge", "skills")

	// Create two skill directories
	deanSkillDir := filepath.Join(skillsDir, "dean-costume")
	err := os.MkdirAll(deanSkillDir, 0755)
	r.NoError(err)

	deanSkill := `---
name: dean-costume
description: Generate creative costume ideas for Dean Pelton
isUserInvocable: true
---

This skill helps generate dalmatian-themed costume variations.
`
	err = os.WriteFile(filepath.Join(deanSkillDir, "SKILL.md"), []byte(deanSkill), 0644)
	r.NoError(err)

	paintballSkillDir := filepath.Join(skillsDir, "paintball-tactics")
	err = os.MkdirAll(paintballSkillDir, 0755)
	r.NoError(err)

	paintballSkill := `---
name: paintball-tactics
description: Strategic paintball combat analysis
isUserInvocable: false
---

Analyzes paintball game strategies from past campus wars.
`
	err = os.WriteFile(filepath.Join(paintballSkillDir, "SKILL.md"), []byte(paintballSkill), 0644)
	r.NoError(err)

	loader := NewLoader(projectDir)
	bundle, err := loader.Load([]string{"project"})
	r.NoError(err)

	r.Len(bundle.SkillDescriptions, 2)

	// Find dean-costume skill
	var deanSkillFound bool
	for _, skill := range bundle.SkillDescriptions {
		if skill.Name == "dean-costume" {
			deanSkillFound = true
			r.Equal("Generate creative costume ideas for Dean Pelton", skill.Description)
			r.True(skill.IsUserInvocable)
		}
	}
	r.True(deanSkillFound)
}

func TestLoader_LoadSkillContent(t *testing.T) {
	r := require.New(t)

	projectDir := t.TempDir()
	skillsDir := filepath.Join(projectDir, ".forge", "skills", "troy-abed")
	err := os.MkdirAll(skillsDir, 0755)
	r.NoError(err)

	skillContent := `---
name: troy-abed
description: Morning show hosting
---

## Troy and Abed in the Morning

This skill provides morning show content generation.
`
	err = os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(skillContent), 0644)
	r.NoError(err)

	loader := NewLoader(projectDir)
	content, err := loader.LoadSkillContent("troy-abed")
	r.NoError(err)
	r.Contains(content, "Troy and Abed in the Morning")
}

func TestLoader_LoadLocalContext(t *testing.T) {
	r := require.New(t)

	projectDir := t.TempDir()

	// Write AGENTS.local.md
	localMD := `# Local overrides

Using the Dreamatorium for testing.
`
	err := os.WriteFile(filepath.Join(projectDir, "AGENTS.local.md"), []byte(localMD), 0644)
	r.NoError(err)

	loader := NewLoader(projectDir)
	bundle, err := loader.Load([]string{"local"})
	r.NoError(err)

	r.Len(bundle.AgentsMD, 1)
	r.Equal("local", bundle.AgentsMD[0].Level)
	r.Contains(bundle.AgentsMD[0].Content, "Dreamatorium")
}

func TestLoader_DiscoverAgents(t *testing.T) {
	r := require.New(t)

	projectDir := t.TempDir()
	agentsDir := filepath.Join(projectDir, ".forge", "agents")
	err := os.MkdirAll(agentsDir, 0755)
	r.NoError(err)

	// Create agent definition
	agentDef := `---
name: abed-agent
description: Film and TV analysis expert
model: claude-sonnet-4-5-20250929
tools:
  - read
  - grep
disallowedTools:
  - bash
maxTurns: 10
---

You are Abed Nadir. Analyze everything through the lens of film and television.
When responding, reference relevant TV tropes and movie parallels.
`
	err = os.WriteFile(filepath.Join(agentsDir, "abed.md"), []byte(agentDef), 0644)
	r.NoError(err)

	loader := NewLoader(projectDir)
	bundle, err := loader.Load([]string{"project"})
	r.NoError(err)

	r.Len(bundle.AgentDefinitions, 1)

	agent, ok := bundle.AgentDefinitions["abed-agent"]
	r.True(ok)
	r.Equal("abed-agent", agent.Name)
	r.Equal("Film and TV analysis expert", agent.Description)
	r.Equal("claude-sonnet-4-5-20250929", agent.Model)
	r.Equal(10, agent.MaxTurns)
	r.Contains(agent.Tools, "read")
	r.Contains(agent.Tools, "grep")
	r.Contains(agent.DisallowedTools, "bash")
	r.Contains(agent.Prompt, "Abed Nadir")
}

func TestLoader_MergeSettings(t *testing.T) {
	r := require.New(t)

	projectDir := t.TempDir()
	forgeDir := filepath.Join(projectDir, ".forge")
	err := os.MkdirAll(forgeDir, 0755)
	r.NoError(err)

	// Write settings.json
	settings := `{
  "model": "claude-opus-4-6",
  "permissions": {
    "allow": ["read", "write"],
    "deny": ["bash"]
  },
  "env": {
    "GREENDALE_MOTTO": "Community College"
  }
}`
	err = os.WriteFile(filepath.Join(forgeDir, "settings.json"), []byte(settings), 0644)
	r.NoError(err)

	// Write settings.local.json
	localSettings := `{
  "model": "claude-sonnet-4-5-20250929",
  "env": {
    "STUDY_GROUP": "Troy and Abed"
  }
}`
	err = os.WriteFile(filepath.Join(forgeDir, "settings.local.json"), []byte(localSettings), 0644)
	r.NoError(err)

	loader := NewLoader(projectDir)
	bundle, err := loader.Load([]string{"project", "local"})
	r.NoError(err)

	// Local overrides project
	r.Equal("claude-sonnet-4-5-20250929", bundle.Settings.Model)

	// Permissions from project
	r.NotNil(bundle.Settings.Permissions)
	r.Contains(bundle.Settings.Permissions.Allow, "read")
	r.Contains(bundle.Settings.Permissions.Deny, "bash")

	// Env merged
	r.Equal("Community College", bundle.Settings.Env["GREENDALE_MOTTO"])
	r.Equal("Troy and Abed", bundle.Settings.Env["STUDY_GROUP"])
}

func TestLoader_ConfigDirFallback(t *testing.T) {
	tests := map[string]struct {
		setup     func(string) error
		wantRules int
	}{
		"forge dir preferred over claude dir": {
			setup: func(dir string) error {
				forgeRules := filepath.Join(dir, ".forge", "rules")
				claudeRules := filepath.Join(dir, ".claude", "rules")
				if err := os.MkdirAll(forgeRules, 0755); err != nil {
					return err
				}
				if err := os.MkdirAll(claudeRules, 0755); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(forgeRules, "forge.md"), []byte("forge rule"), 0644); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(claudeRules, "claude.md"), []byte("claude rule"), 0644)
			},
			wantRules: 1, // only .forge/ rules, not .claude/
		},
		"claude dir used when no forge dir": {
			setup: func(dir string) error {
				claudeRules := filepath.Join(dir, ".claude", "rules")
				if err := os.MkdirAll(claudeRules, 0755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(claudeRules, "legacy.md"), []byte("legacy rule"), 0644)
			},
			wantRules: 1,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			tmpDir := t.TempDir()

			err := tc.setup(tmpDir)
			r.NoError(err)

			loader := NewLoader(tmpDir)
			bundle, err := loader.Load([]string{"project"})
			r.NoError(err)

			r.Len(bundle.Rules, tc.wantRules)
		})
	}
}
