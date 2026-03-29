package prompt

import (
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAssemble_BasePrompt(t *testing.T) {
	r := require.New(t)

	bundle := types.ContextBundle{}
	blocks := Assemble(bundle, "/home/troy/greendale")

	// Should have at least base prompt and environment info
	r.GreaterOrEqual(len(blocks), 2)
	r.Contains(blocks[0].Text, "helpful coding assistant")
	r.Contains(blocks[1].Text, "/home/troy/greendale")
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

	// Find the CLAUDE.md block
	var claudeBlock *types.SystemBlock
	for _, block := range blocks {
		if block.CacheControl != nil {
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

	// Should have: base prompt, env info, CLAUDE.md, rules, skills, agents = 6 blocks
	r.Equal(6, len(blocks))
}
