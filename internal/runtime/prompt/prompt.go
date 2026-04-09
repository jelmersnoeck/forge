// Package prompt assembles system prompts from context bundles.
package prompt

import (
	"cmp"
	"fmt"
	"runtime"
	"slices"
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

Self-improvement: Use the Reflect tool at session end ONLY if you discovered actionable gotchas, workarounds, or non-obvious behaviors. Skip reflection if the session was routine with no new insights. Learnings load into future sessions from .forge/learnings/.

Response format:
- Noun phrases only for actions ("Reading file", "Running tests", "Writing implementation")
- No conversational filler ("Good!", "Let's", "Now", "Great")
- No rhetorical questions or explanations unless user asks
- State action, execute, report result
- Example: "Writing test" not "Good! Now let's create a simple test to verify our changes work"
- Minimal tokens in/out
- No emoji, exclamations, pleasantries`

const specPrompt = `
## Spec-Driven Development

Before implementing any feature, write a specification first. The spec acts as
the source of truth for implementation, acceptance testing, and intent validation.

### Workflow

1. User describes a feature → write a spec to the specs directory
2. Review the spec (confirm scope, constraints, edge cases)
3. Implement against the spec
4. Validate implementation matches spec's Behavior and Edge Cases
5. Reconcile the spec (see below)

If the user provides a spec via --spec, skip step 1 and implement directly.

### Spec Format

Specs are markdown with YAML frontmatter, stored in the specs directory
(default: .forge/specs/, configurable via .forge/config.json "specsDir").

` + "```" + `markdown
---
id: feature-slug
status: draft
---
# Summary (max 15 words)

## Description
Short description. 2-4 sentences.

## Context
Files, systems, interfaces that change. Be specific — paths, functions, types.

## Behavior
Desired behaviour and UX. Each point is a potential acceptance test.
Include flags, endpoints, messages, etc.

## Constraints
Things to avoid. Falsifiable: "don't do X" not "be careful with X".

## Interfaces
Types, signatures, schemas. Use code blocks.

## Edge Cases
Scenario + expected outcome for each.
` + "```" + `

### Rules

- Header: 15 words max
- ID: lowercase kebab-case, used as filename
- New specs start as status: draft
- Set to active when approved, implemented when done

### Spec Reconciliation

IMPORTANT: Before finishing your work, you MUST reconcile the spec.

During a session the user may send follow-up messages that correct, refine, or
redirect the implementation. These messages change the intent but the spec file
still reflects the original request. The spec must be the single source of truth
for what was built and why.

Before your final response in any implementation session:

1. Identify the spec file for this session (the one you wrote or were given).
2. Review ALL user messages in the conversation. Look for:
   - Corrections ("actually, use X instead of Y")
   - Added requirements ("also add a --verbose flag")
   - Removed requirements ("skip the caching for now")
   - Clarifications that narrowed or widened scope
   - Constraint changes ("don't worry about backwards compat")
3. Update the spec file using the Edit tool:
   - Amend Behavior to match what was actually implemented
   - Amend Constraints to reflect actual constraints applied
   - Amend Interfaces to match actual types/signatures built
   - Amend Edge Cases with any new ones discovered during implementation
   - Update Context with any files that were touched but not originally listed
   - Keep Description accurate — if scope changed, say so
4. Do NOT change the ID or remove sections. Add, amend, refine.
5. Set status to "active" (or "implemented" if everything is done and tested).

The result: a reviewer reading only the spec should understand the full intent
of what was built, including all mid-session course corrections. No conversation
archaeology required.

If there were no user corrections (single-prompt session), still verify the spec
matches what was implemented — types may have evolved, edge cases may have been
discovered, files may have been added.
`

// Assemble creates the system prompt blocks from a context bundle.
// Max 4 cache_control blocks total across system + tools + messages.
// Strategy: 2 system blocks + 1 tool + 1 message = 4 total
func Assemble(bundle types.ContextBundle, cwd string) []types.SystemBlock {
	var blocks []types.SystemBlock

	// Split AgentsMD into "instructions" (user/project/local/parent) and
	// "learnings" (.forge/learnings/* files). Instructions go in the static
	// block; learnings go in the dynamic block.
	var instructions, learnings []types.AgentsMDEntry
	for _, entry := range bundle.AgentsMD {
		switch {
		case strings.Contains(entry.Path, ".forge/learnings/"):
			learnings = append(learnings, entry)
		default:
			instructions = append(instructions, entry)
		}
	}

	// 1. Base prompt + environment info + project instructions (static, global cache)
	// Merged into one block to free up cache slots for message-level caching
	var staticContent strings.Builder
	staticContent.WriteString(basePrompt)
	staticContent.WriteString(specPrompt)
	fmt.Fprintf(&staticContent, "\n\nEnvironment Information:\n- Working directory: %s\n- Platform: %s\n- Current date: %s",
		cwd, runtime.GOOS, time.Now().Format("2006-01-02"))

	if len(instructions) > 0 {
		staticContent.WriteString("\n\n<system-reminder>\n")
		staticContent.WriteString("Project and user instructions are shown below. Follow these instructions carefully.\n\n")

		for _, entry := range instructions {
			fmt.Fprintf(&staticContent, "## From %s (%s)\n\n", entry.Path, entry.Level)
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

	// 2. Dynamic content: learnings + Rules + Skills + Agent definitions
	// This is the only other system block, freeing up cache slots for message-level caching
	// System blocks (2) + Tools (1) + Messages (1) = 4 total cache_control blocks
	var bundledContent strings.Builder
	hasContent := false

	// Learnings from .forge/learnings/
	if len(learnings) > 0 {
		bundledContent.WriteString("<system-reminder>\n")
		bundledContent.WriteString("Self-improvement learnings from previous sessions:\n\n")
		for _, entry := range learnings {
			fmt.Fprintf(&bundledContent, "## From %s (%s)\n\n", entry.Path, entry.Level)
			bundledContent.WriteString(entry.Content)
			bundledContent.WriteString("\n\n")
		}
		bundledContent.WriteString("Before starting work, scan the learnings above for anything relevant to the current task. If a learning applies, factor it into your approach.\n")
		bundledContent.WriteString("</system-reminder>\n\n")
		hasContent = true
	}

	// Rules
	if len(bundle.Rules) > 0 {
		bundledContent.WriteString("<system-reminder>\n")
		bundledContent.WriteString("Additional rules and guidelines:\n\n")
		for _, rule := range bundle.Rules {
			fmt.Fprintf(&bundledContent, "## Rule: %s\n\n", rule.Path)
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
			fmt.Fprintf(&bundledContent, "- **%s** (%s): %s\n", skill.Name, invocable, skill.Description)
		}
		bundledContent.WriteString("\n")
		hasContent = true
	}

	// Agent definitions (sorted for deterministic output / prompt caching)
	if len(bundle.AgentDefinitions) > 0 {
		bundledContent.WriteString("Available Agents:\n\n")
		names := make([]string, 0, len(bundle.AgentDefinitions))
		for name := range bundle.AgentDefinitions {
			names = append(names, name)
		}
		slices.SortFunc(names, func(a, b string) int {
			return cmp.Compare(a, b)
		})
		for _, name := range names {
			agent := bundle.AgentDefinitions[name]
			fmt.Fprintf(&bundledContent, "- **%s**: %s\n", name, agent.Description)
			if agent.Model != "" {
				fmt.Fprintf(&bundledContent, "  Model: %s\n", agent.Model)
			}
			if len(agent.Tools) > 0 {
				fmt.Fprintf(&bundledContent, "  Tools: %s\n", strings.Join(agent.Tools, ", "))
			}
			if agent.MaxTurns > 0 {
				fmt.Fprintf(&bundledContent, "  Max turns: %d\n", agent.MaxTurns)
			}
		}
		hasContent = true
	}

	// Active specs
	if len(bundle.Specs) > 0 {
		var activeSpecs []string
		for _, s := range bundle.Specs {
			if s.Status == "active" {
				activeSpecs = append(activeSpecs, fmt.Sprintf("- **%s**: %s", s.ID, s.Header))
			}
		}
		if len(activeSpecs) > 0 {
			bundledContent.WriteString("Active Specs:\n\n")
			for _, line := range activeSpecs {
				bundledContent.WriteString(line)
				bundledContent.WriteString("\n")
			}
			bundledContent.WriteString("\n")
			hasContent = true
		}
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
