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

	// Should have base prompt + spec prompt + env info merged into one block
	r.GreaterOrEqual(len(blocks), 1)
	r.Contains(blocks[0].Text, "Coding assistant")
	r.Contains(blocks[0].Text, "/home/troy/greendale")
	r.Contains(blocks[0].Text, "Spec-Driven Development")
	r.Contains(blocks[0].Text, "forge/specs")
	r.Contains(blocks[0].Text, "Spec Reconciliation")
	r.Contains(blocks[0].Text, "MUST reconcile the spec")
}

func TestAssemble_DocumentationSection_NotInBaseOrSpecPrompt(t *testing.T) {
	r := require.New(t)

	// Assemble with no phase prompt — base + spec only
	bundle := types.ContextBundle{}
	blocks := Assemble(bundle, "/greendale/community-college")
	r.GreaterOrEqual(len(blocks), 1)
	r.NotContains(blocks[0].Text, "## Documentation",
		"base/spec prompt should not contain Documentation section")
}

func TestAssemble_AgentsMD_Instructions(t *testing.T) {
	r := require.New(t)

	bundle := types.ContextBundle{
		AgentsMD: []types.AgentsMDEntry{
			{
				Path:    "/home/troy/AGENTS.md",
				Content: "# Troy Barnes Rules\n\nAlways be cool. Cool cool cool.",
				Level:   "user",
			},
			{
				Path:    "/home/troy/greendale/AGENTS.md",
				Content: "# Study Group Guidelines\n\nNo Pierce.",
				Level:   "project",
			},
		},
	}

	blocks := Assemble(bundle, "/home/troy/greendale")

	// Find the instructions block (static block with system-reminder)
	var instructionsBlock *types.SystemBlock
	for _, block := range blocks {
		if block.CacheControl != nil && strings.Contains(block.Text, "Troy Barnes Rules") {
			instructionsBlock = &block
			break
		}
	}

	r.NotNil(instructionsBlock)
	r.Contains(instructionsBlock.Text, "Troy Barnes Rules")
	r.Contains(instructionsBlock.Text, "Study Group Guidelines")
	r.Contains(instructionsBlock.Text, "<system-reminder>")
	r.Equal("ephemeral", instructionsBlock.CacheControl.Type)
}

func TestAssemble_AgentsMD_Learnings(t *testing.T) {
	r := require.New(t)

	bundle := types.ContextBundle{
		AgentsMD: []types.AgentsMDEntry{
			{
				Path:    "/test/.forge/learnings/20260404-troy-session.md",
				Content: "# Troy session\n\nLearned plumbing.",
				Level:   "project",
			},
		},
	}

	blocks := Assemble(bundle, "/test")

	// Learnings go into the dynamic (bundled) block
	r.GreaterOrEqual(len(blocks), 2)
	r.Contains(blocks[1].Text, "Self-improvement learnings")
	r.Contains(blocks[1].Text, "Troy session")
	r.Contains(blocks[1].Text, "scan the learnings above")
}

func TestAssemble_Rules(t *testing.T) {
	r := require.New(t)

	bundle := types.ContextBundle{
		Rules: []types.RuleEntry{
			{
				Path:    "/home/troy/greendale/.forge/rules/paintball.md",
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
		AgentsMD: []types.AgentsMDEntry{
			{Path: "/test/AGENTS.md", Content: "Test instructions", Level: "project"},
		},
		Rules: []types.RuleEntry{
			{Path: "/test/.forge/rules/test.md", Content: "Test rule", Level: "project"},
		},
		SkillDescriptions: []types.SkillDescription{
			{Name: "test-skill", Description: "Test", IsUserInvocable: true},
		},
		AgentDefinitions: map[string]types.AgentDefinition{
			"test-agent": {Name: "test-agent", Description: "Test agent"},
		},
	}

	blocks := Assemble(bundle, "/test")

	// Should have: static(base+env+instructions), bundled(rules+skills+agents) = 2 blocks
	// This frees up cache slots for message-level caching (system 2 + tools 1 + messages 1 = 4)
	r.Equal(2, len(blocks))

	// Static block should contain base, env, and instructions
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
		AgentsMD: []types.AgentsMDEntry{
			{Path: "/test/AGENTS.md", Content: "Test AGENTS.md", Level: "project"},
			{Path: "/test/.forge/learnings/session.md", Content: "Test learnings", Level: "project"},
		},
		Rules: []types.RuleEntry{
			{Path: "/test/.forge/rules/test.md", Content: "Test rule", Level: "project"},
		},
		SkillDescriptions: []types.SkillDescription{
			{Name: "test-skill", Description: "Test", IsUserInvocable: true},
		},
		AgentDefinitions: map[string]types.AgentDefinition{
			"test-agent": {Name: "test-agent", Description: "Test agent"},
		},
	}

	blocks := Assemble(bundle, "/test")

	// Should have: static(base+env+instructions), bundled(learnings+rules+skills+agents) = 2 blocks
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
	r.Contains(staticBlock.Text, "AGENTS.md")
	r.Equal("global", staticBlock.CacheControl.Scope)
}

func TestAssemble_AgentDefinitions_DeterministicOrder(t *testing.T) {
	// Agent definitions are stored in a map. Without sorting, the prompt text
	// changes every turn, busting the Anthropic prompt cache.
	r := require.New(t)

	bundle := types.ContextBundle{
		AgentDefinitions: map[string]types.AgentDefinition{
			"troy":    {Name: "troy", Description: "Football and plumbing"},
			"abed":    {Name: "abed", Description: "Film and TV analysis"},
			"britta":  {Name: "britta", Description: "The worst"},
			"jeff":    {Name: "jeff", Description: "Lawyer turned student"},
			"shirley": {Name: "shirley", Description: "Baking and judgment"},
		},
	}

	// Get canonical output
	canonical := Assemble(bundle, "/greendale")
	r.GreaterOrEqual(len(canonical), 2)
	canonicalText := canonical[1].Text

	// Must contain agents in alphabetical order
	abedIdx := strings.Index(canonicalText, "abed")
	brittaIdx := strings.Index(canonicalText, "britta")
	jeffIdx := strings.Index(canonicalText, "jeff")
	shirleyIdx := strings.Index(canonicalText, "shirley")
	troyIdx := strings.Index(canonicalText, "troy")

	r.Greater(brittaIdx, abedIdx, "agents not sorted: britta before abed")
	r.Greater(jeffIdx, brittaIdx, "agents not sorted: jeff before britta")
	r.Greater(shirleyIdx, jeffIdx, "agents not sorted: shirley before jeff")
	r.Greater(troyIdx, shirleyIdx, "agents not sorted: troy before shirley")

	// Run 50 more times — must be byte-identical for cache stability
	for i := 0; i < 50; i++ {
		blocks := Assemble(bundle, "/greendale")
		r.Equal(canonicalText, blocks[1].Text, "iteration %d: prompt text changed", i)
	}
}

func TestAssemble_Specs(t *testing.T) {
	r := require.New(t)

	bundle := types.ContextBundle{
		Specs: []types.SpecEntry{
			{ID: "paintball-defense", Status: "active", Header: "Campus-wide paintball defense system"},
			{ID: "pillow-fort", Status: "draft", Header: "Blanket fort vs pillow fort warfare"},
			{ID: "darkest-timeline", Status: "deprecated", Header: "Goatee-based timeline detection"},
			{ID: "study-room-f", Status: "active", Header: "Study room scheduling and booking"},
			{ID: "old-feature", Status: "implemented", Header: "Something already done"},
			{ID: "dead-spec", Status: "superseded", Header: "Replaced by something better"},
		},
	}

	blocks := Assemble(bundle, "/greendale")
	r.GreaterOrEqual(len(blocks), 2)

	bundled := blocks[1].Text
	r.Contains(bundled, "Existing Specs:")

	// All specs included regardless of status
	r.Contains(bundled, "paintball-defense")
	r.Contains(bundled, "pillow-fort")
	r.Contains(bundled, "darkest-timeline")
	r.Contains(bundled, "study-room-f")
	r.Contains(bundled, "old-feature")
	r.Contains(bundled, "dead-spec")

	// Status is shown in parentheses
	r.Contains(bundled, "(active)")
	r.Contains(bundled, "(draft)")
	r.Contains(bundled, "(deprecated)")
	r.Contains(bundled, "(implemented)")
	r.Contains(bundled, "(superseded)")
}

func TestAssemble_Specs_NoneActive(t *testing.T) {
	r := require.New(t)

	bundle := types.ContextBundle{
		Specs: []types.SpecEntry{
			{ID: "old-feature", Status: "implemented", Header: "Something already done"},
		},
	}

	blocks := Assemble(bundle, "/greendale")
	// Implemented specs still show in the index (for dedup purposes)
	r.Equal(2, len(blocks))
	r.Contains(blocks[1].Text, "Existing Specs:")
	r.Contains(blocks[1].Text, "old-feature")
	r.Contains(blocks[1].Text, "(implemented)")
}
