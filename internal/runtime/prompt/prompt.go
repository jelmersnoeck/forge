// Package prompt assembles system prompts from context bundles.
package prompt

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
)

const basePrompt = `Coding assistant with file/search/command/edit tools.

Principles:
- Think before acting
- Read code before changing
- Test changes
- Ask when unclear
- Maximize brevity

Tools: Read, Grep, Bash, Edit (modify), Write (new files only)

Self-improvement: Reflect at session end → AGENTS.md → loads next session

Response format:
- Noun phrases only for actions ("Reading file", "Running tests", "Writing implementation")
- No conversational filler ("Good!", "Let's", "Now", "Great")
- No rhetorical questions or explanations unless user asks
- State action, execute, report result
- Example: "Writing test" not "Good! Now let's create a simple test to verify our changes work"
- Minimal tokens in/out
- No emoji, exclamations, pleasantries`

// Assemble creates the system prompt blocks from a context bundle.
// Max 4 cache_control blocks total across system + tools + messages.
// Strategy: 2 system blocks + 1 tool + 1 message = 4 total
func Assemble(bundle types.ContextBundle, cwd string) []types.SystemBlock {
	var blocks []types.SystemBlock

	// 1. Base prompt + environment info + CLAUDE.md (static, global cache)
	// Merged into one block to free up cache slots for message-level caching
	var staticContent strings.Builder
	staticContent.WriteString(basePrompt)
	staticContent.WriteString(fmt.Sprintf("\n\nEnvironment Information:\n- Working directory: %s\n- Platform: %s\n- Current date: %s",
		cwd, runtime.GOOS, time.Now().Format("2006-01-02")))

	if len(bundle.ClaudeMD) > 0 {
		staticContent.WriteString("\n\n<system-reminder>\n")
		staticContent.WriteString("Project and user instructions are shown below. Follow these instructions carefully.\n\n")

		for _, entry := range bundle.ClaudeMD {
			staticContent.WriteString(fmt.Sprintf("## From %s (%s)\n\n", entry.Path, entry.Level))
			staticContent.WriteString(entry.Content)
			staticContent.WriteString("\n\n")
		}

		staticContent.WriteString("</system-reminder>")
	}

	blocks = append(blocks, types.SystemBlock{
		Type: "text",
		Text: staticContent.String(),
		CacheControl: &types.CacheControl{
			Type:  "ephemeral",
			TTL:   "1h",
			Scope: "global", // Static content shared across sessions
		},
	})

	// 2. Dynamic content: AGENTS.md + Rules + Skills + Agent definitions
	// This is the only other system block, freeing up cache slots for message-level caching
	// System blocks (2) + Tools (1) + Messages (1) = 4 total cache_control blocks
	var bundledContent strings.Builder
	hasContent := false

	// AGENTS.md learnings
	if len(bundle.AgentsMD) > 0 {
		bundledContent.WriteString("<system-reminder>\n")
		bundledContent.WriteString("Self-improvement learnings from previous sessions:\n\n")
		for _, entry := range bundle.AgentsMD {
			bundledContent.WriteString(fmt.Sprintf("## From %s (%s)\n\n", entry.Path, entry.Level))
			bundledContent.WriteString(entry.Content)
			bundledContent.WriteString("\n\n")
		}
		bundledContent.WriteString("</system-reminder>\n\n")
		hasContent = true
	}

	// Rules
	if len(bundle.Rules) > 0 {
		bundledContent.WriteString("<system-reminder>\n")
		bundledContent.WriteString("Additional rules and guidelines:\n\n")
		for _, rule := range bundle.Rules {
			bundledContent.WriteString(fmt.Sprintf("## Rule: %s\n\n", rule.Path))
			bundledContent.WriteString(rule.Content)
			bundledContent.WriteString("\n\n")
		}
		bundledContent.WriteString("</system-reminder>\n\n")
		hasContent = true
	}

	// Skills
	if len(bundle.SkillDescriptions) > 0 {
		bundledContent.WriteString("Available Skills:\n\n")
		for _, skill := range bundle.SkillDescriptions {
			invocable := "system-only"
			if skill.IsUserInvocable {
				invocable = "user-invocable"
			}
			bundledContent.WriteString(fmt.Sprintf("- **%s** (%s): %s\n", skill.Name, invocable, skill.Description))
		}
		bundledContent.WriteString("\n")
		hasContent = true
	}

	// Agent definitions
	if len(bundle.AgentDefinitions) > 0 {
		bundledContent.WriteString("Available Agents:\n\n")
		for name, agent := range bundle.AgentDefinitions {
			bundledContent.WriteString(fmt.Sprintf("- **%s**: %s\n", name, agent.Description))
			if agent.Model != "" {
				bundledContent.WriteString(fmt.Sprintf("  Model: %s\n", agent.Model))
			}
			if len(agent.Tools) > 0 {
				bundledContent.WriteString(fmt.Sprintf("  Tools: %s\n", strings.Join(agent.Tools, ", ")))
			}
			if agent.MaxTurns > 0 {
				bundledContent.WriteString(fmt.Sprintf("  Max turns: %d\n", agent.MaxTurns))
			}
		}
		hasContent = true
	}

	// Add bundled block with cache control if we have any content
	if hasContent {
		blocks = append(blocks, types.SystemBlock{
			Type: "text",
			Text: strings.TrimSpace(bundledContent.String()),
			CacheControl: &types.CacheControl{
				Type: "ephemeral",
				TTL:  "1h",
			},
		})
	}

	return blocks
}
