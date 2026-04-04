package prompt

import (
	"strings"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAssemble_BasePrompt(t *testing.T) {
	r := require.New(t)

	bundle := types.ContextBundle{}
	blocks := Assemble(bundle, "/home/troy/greendale")

	// Should have base prompt + env info merged into one block
	r.GreaterOrEqual(len(blocks), 1)
	r.Contains(blocks[0].Text, "Coding assistant")
	r.Contains(blocks[0].Text, "/home/troy/greendale")
}

func TestAssemble_ClaudeMD(t *testing.T) {
	r := require.New(t)

	bundle := types.ContextBundle{
		ClaudeMD: []types.ClaudeMDEntry{
			{
				Path:    "/home/troy/.claude/CLAUDE.md",
				Content: "# Troy Barnes Rules\n\nAlways be cool. Cool cool cool.",
				Level:   "user",
			},
			{
				Path:    "/home/troy/greendale/CLAUDE.md",
				Content: "# Study Group Guidelines\n\nNo Pierce.",
				Level:   "project",
			},
		},
	}

	blocks := Assemble(bundle, "/home/troy/greendale")

	// Find the CLAUDE.md block (contains system-reminder and CLAUDE.md content)
	var claudeBlock *types.SystemBlock
	for _, block := range blocks {
		if block.CacheControl != nil && strings.Contains(block.Text, "Troy Barnes Rules") {
			claudeBlock = &block
			break
		}
	}

	r.NotNil(claudeBlock)
	r.Contains(claudeBlock.Text, "Troy Barnes Rules")
	r.Contains(claudeBlock.Text, "Study Group Guidelines")
	r.Contains(claudeBlock.Text, "<system-reminder>")
	r.Equal("ephemeral", claudeBlock.CacheControl.Type)
}

func TestAssemble_Rules(t *testing.T) {
	r := require.New(t)

	bundle := types.ContextBundle{
		Rules: []types.RuleEntry{
			{
				Path:    "/home/troy/greendale/.claude/rules/paintball.md",
				Content: "No paintball during finals week.",
				Level:   "project",
			},
		},
	}

	blocks := Assemble(bundle, "/home/troy/greendale")

	// Find the rules block
	var rulesBlock *types.SystemBlock
	for _, block := range blocks {
		if len(block.Text) > 0 && block.Text[0:17] == "<system-reminder>" {
			rulesBlock = &block
			break
		}
	}

	r.NotNil(rulesBlock)
	r.Contains(rulesBlock.Text, "Additional rules")
	r.Contains(rulesBlock.Text, "No paintball")
}

func TestAssemble_Skills(t *testing.T) {
	r := require.New(t)

	bundle := types.ContextBundle{
		SkillDescriptions: []types.SkillDescription{
			{
				Name:            "dean-costume",
				Description:     "Generate Dean Pelton costume ideas",
				IsUserInvocable: true,
			},
			{
				Name:            "paintball-tactics",
				Description:     "Strategic paintball analysis",
				IsUserInvocable: false,
			},
		},
	}

	blocks := Assemble(bundle, "/home/troy/greendale")

	// Find the skills block
	var skillsBlock *types.SystemBlock
	for _, block := range blocks {
		if len(block.Text) > 10 && block.Text[0:17] == "Available Skills:" {
			skillsBlock = &block
			break
		}
	}

	r.NotNil(skillsBlock)
	r.Contains(skillsBlock.Text, "dean-costume")
	r.Contains(skillsBlock.Text, "user-invocable")
	r.Contains(skillsBlock.Text, "paintball-tactics")
	r.Contains(skillsBlock.Text, "system-only")
}

func TestAssemble_Agents(t *testing.T) {
	r := require.New(t)

	bundle := types.ContextBundle{
		AgentDefinitions: map[string]types.AgentDefinition{
			"abed": {
				Name:        "abed",
				Description: "Film and TV analysis",
				Model:       "claude-sonnet-4-5-20250929",
				Tools:       []string{"read", "grep"},
				MaxTurns:    10,
			},
		},
	}

	blocks := Assemble(bundle, "/home/troy/greendale")

	// Find the agents block
	var agentsBlock *types.SystemBlock
	for _, block := range blocks {
		if len(block.Text) > 10 && block.Text[0:17] == "Available Agents:" {
			agentsBlock = &block
			break
		}
	}

	r.NotNil(agentsBlock)
	r.Contains(agentsBlock.Text, "abed")
	r.Contains(agentsBlock.Text, "Film and TV analysis")
	r.Contains(agentsBlock.Text, "claude-sonnet-4-5-20250929")
	r.Contains(agentsBlock.Text, "read, grep")
	r.Contains(agentsBlock.Text, "Max turns: 10")
}

func TestAssemble_AllFeatures(t *testing.T) {
	r := require.New(t)

	bundle := types.ContextBundle{
		ClaudeMD: []types.ClaudeMDEntry{
			{Path: "/test/CLAUDE.md", Content: "Test instructions", Level: "project"},
		},
		Rules: []types.RuleEntry{
			{Path: "/test/.claude/rules/test.md", Content: "Test rule", Level: "project"},
		},
		SkillDescriptions: []types.SkillDescription{
			{Name: "test-skill", Description: "Test", IsUserInvocable: true},
		},
		AgentDefinitions: map[string]types.AgentDefinition{
			"test-agent": {Name: "test-agent", Description: "Test agent"},
		},
	}

	blocks := Assemble(bundle, "/test")

	// Should have: static(base+env+CLAUDE.md), bundled(rules+skills+agents) = 2 blocks
	// This frees up cache slots for message-level caching (system 2 + tools 1 + messages 1 = 4)
	r.Equal(2, len(blocks))

	// Static block should contain base, env, and CLAUDE.md
	staticBlock := blocks[0]
	r.Contains(staticBlock.Text, "Coding assistant")
	r.Contains(staticBlock.Text, "Working directory: /test")
	r.Contains(staticBlock.Text, "Test instructions")
	r.NotNil(staticBlock.CacheControl)
	r.Equal("global", staticBlock.CacheControl.Scope)

	// Bundled content contains all dynamic sections
	bundledBlock := blocks[1]
	r.Contains(bundledBlock.Text, "Test rule")
	r.Contains(bundledBlock.Text, "test-skill")
	r.Contains(bundledBlock.Text, "test-agent")
}

func TestAssemble_CacheControlTTL(t *testing.T) {
	r := require.New(t)

	bundle := types.ContextBundle{
		ClaudeMD: []types.ClaudeMDEntry{
			{Path: "/test/CLAUDE.md", Content: "Test CLAUDE.md", Level: "project"},
		},
		AgentsMD: []types.AgentsMDEntry{
			{Path: "/test/AGENTS.md", Content: "Test AGENTS.md", Level: "project"},
		},
		Rules: []types.RuleEntry{
			{Path: "/test/.claude/rules/test.md", Content: "Test rule", Level: "project"},
		},
		SkillDescriptions: []types.SkillDescription{
			{Name: "test-skill", Description: "Test", IsUserInvocable: true},
		},
		AgentDefinitions: map[string]types.AgentDefinition{
			"test-agent": {Name: "test-agent", Description: "Test agent"},
		},
	}

	blocks := Assemble(bundle, "/test")

	// Should have: static(base+env+CLAUDE.md), bundled(AGENTS.md+rules+skills+agents) = 2 blocks
	// This leaves room for: system(2) + tools(1) + messages(1) = 4 cache_control blocks total
	blockNames := []string{"static", "bundled"}
	r.Equal(len(blockNames), len(blocks), "expected %d blocks", len(blockNames))

	for i, block := range blocks {
		r.NotNil(block.CacheControl, "block %d (%s) missing cache control", i, blockNames[i])
		r.Equal("ephemeral", block.CacheControl.Type, "block %d (%s) wrong cache type", i, blockNames[i])
		r.Equal("1h", block.CacheControl.TTL, "block %d (%s) wrong TTL", i, blockNames[i])
	}

	// Static block should have global scope (shared across sessions)
	staticBlock := blocks[0]
	r.Contains(staticBlock.Text, "CLAUDE.md")
	r.Equal("global", staticBlock.CacheControl.Scope)
}
