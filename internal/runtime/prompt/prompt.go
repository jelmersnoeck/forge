// Package prompt assembles system prompts from context bundles.
package prompt

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
)

const basePrompt = `You are a helpful coding assistant. You have access to tools that allow you to read files, search code, run commands, and modify files.

Your goal is to help the user complete their coding tasks efficiently and correctly.

Key principles:
- Think carefully before taking action
- Prefer reading and understanding code before making changes
- Test changes when possible
- Ask for clarification when requirements are unclear
- Be concise in your responses

When using tools:
- Use Read to examine files
- Use Grep to search for patterns
- Use Bash to run commands (git, tests, builds)
- Use Edit to modify files
- Use Write only for new files

Always explain what you're doing and why.`

// Assemble creates the system prompt blocks from a context bundle.
func Assemble(bundle types.ContextBundle, cwd string) []types.SystemBlock {
	var blocks []types.SystemBlock

	// 1. Base coding agent prompt
	blocks = append(blocks, types.SystemBlock{
		Type: "text",
		Text: basePrompt,
	})

	// 2. Environment info
	envInfo := fmt.Sprintf(`Environment Information:
- Working directory: %s
- Platform: %s
- Current date: %s`, cwd, runtime.GOOS, time.Now().Format("2006-01-02"))

	blocks = append(blocks, types.SystemBlock{
		Type: "text",
		Text: envInfo,
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
				Type: "ephemeral",
			},
		})
	}

	// 4. Rules wrapped in <system-reminder> tags
	if len(bundle.Rules) > 0 {
		var rulesContent strings.Builder
		rulesContent.WriteString("<system-reminder>\n")
		rulesContent.WriteString("Additional rules and guidelines:\n\n")

		for _, rule := range bundle.Rules {
			rulesContent.WriteString(fmt.Sprintf("## Rule: %s\n\n", rule.Path))
			rulesContent.WriteString(rule.Content)
			rulesContent.WriteString("\n\n")
		}

		rulesContent.WriteString("</system-reminder>")

		blocks = append(blocks, types.SystemBlock{
			Type: "text",
			Text: rulesContent.String(),
		})
	}

	// 5. Skill descriptions
	if len(bundle.SkillDescriptions) > 0 {
		var skillsContent strings.Builder
		skillsContent.WriteString("Available Skills:\n\n")

		for _, skill := range bundle.SkillDescriptions {
			invocable := "system-only"
			if skill.IsUserInvocable {
				invocable = "user-invocable"
			}
			skillsContent.WriteString(fmt.Sprintf("- **%s** (%s): %s\n", skill.Name, invocable, skill.Description))
		}

		blocks = append(blocks, types.SystemBlock{
			Type: "text",
			Text: skillsContent.String(),
		})
	}

	// 6. Agent descriptions
	if len(bundle.AgentDefinitions) > 0 {
		var agentsContent strings.Builder
		agentsContent.WriteString("Available Agents:\n\n")

		for name, agent := range bundle.AgentDefinitions {
			agentsContent.WriteString(fmt.Sprintf("- **%s**: %s\n", name, agent.Description))
			if agent.Model != "" {
				agentsContent.WriteString(fmt.Sprintf("  Model: %s\n", agent.Model))
			}
			if len(agent.Tools) > 0 {
				agentsContent.WriteString(fmt.Sprintf("  Tools: %s\n", strings.Join(agent.Tools, ", ")))
			}
			if agent.MaxTurns > 0 {
				agentsContent.WriteString(fmt.Sprintf("  Max turns: %d\n", agent.MaxTurns))
			}
		}

		blocks = append(blocks, types.SystemBlock{
			Type: "text",
			Text: agentsContent.String(),
		})
	}

	return blocks
}
