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

Response style:
- Minimal tokens in/out
- Report only essential info
- Skip acknowledgments/pleasantries
- State what, not why (unless critical)
- No emoji, exclamations, filler`

// Assemble creates the system prompt blocks from a context bundle.
func Assemble(bundle types.ContextBundle, cwd string) []types.SystemBlock {
	var blocks []types.SystemBlock

	// 1. Base coding agent prompt (cached - never changes)
	// Uses 1h TTL because this content is completely static.
	// API requires TTL ordering: 1h blocks must come before 5m blocks
	// across tools → system → messages.
	blocks = append(blocks, types.SystemBlock{
		Type: "text",
		Text: basePrompt,
		CacheControl: &types.CacheControl{
			Type: "ephemeral",
			TTL:  "1h",
		},
	})

	// 2. Environment info (cached - only changes daily when date changes)
	envInfo := fmt.Sprintf(`Environment Information:
- Working directory: %s
- Platform: %s
- Current date: %s`, cwd, runtime.GOOS, time.Now().Format("2006-01-02"))

	blocks = append(blocks, types.SystemBlock{
		Type: "text",
		Text: envInfo,
		CacheControl: &types.CacheControl{
			Type: "ephemeral",
			TTL:  "1h",
		},
	})

	// 3. CLAUDE.md content wrapped in <system-reminder> tags
	if len(bundle.ClaudeMD) > 0 {
		var claudeContent strings.Builder
		claudeContent.WriteString("<system-reminder>\n")
		claudeContent.WriteString("Project and user instructions are shown below. Follow these instructions carefully.\n\n")

		for _, entry := range bundle.ClaudeMD {
			claudeContent.WriteString(fmt.Sprintf("## From %s (%s)\n\n", entry.Path, entry.Level))
			claudeContent.WriteString(entry.Content)
			claudeContent.WriteString("\n\n")
		}

		claudeContent.WriteString("</system-reminder>")

		blocks = append(blocks, types.SystemBlock{
			Type: "text",
			Text: claudeContent.String(),
			CacheControl: &types.CacheControl{
				Type:  "ephemeral",
				TTL:   "1h",      // Extended cache lifetime (default is 5min)
				Scope: "global",  // Share cache across sessions (safe for CLAUDE.md)
			},
		})
	}

	// 4. Bundle: AGENTS.md + Rules + Skills + Agent definitions (cached together)
	// This keeps us under the 4 cache control block limit while still caching everything
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
			},
		})
	}

	return blocks
}
