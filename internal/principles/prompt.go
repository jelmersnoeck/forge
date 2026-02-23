package principles

import (
	"fmt"
	"strings"
)

// AssemblePrompt builds a prompt section from a list of principles,
// formatted for inclusion in an agent prompt.
func AssemblePrompt(principles []Principle) string {
	if len(principles) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Principles\n\n")
	b.WriteString("You MUST evaluate the code against each of the following principles.\n")
	b.WriteString("For each principle, report whether it passes or has findings.\n\n")

	for _, p := range principles {
		fmt.Fprintf(&b, "### [%s] %s (severity: %s)\n", p.ID, p.Title, p.Severity)
		fmt.Fprintf(&b, "**Category:** %s\n", p.Category)
		fmt.Fprintf(&b, "**Description:** %s\n", p.Description)
		if p.Rationale != "" {
			fmt.Fprintf(&b, "**Rationale:** %s\n", p.Rationale)
		}
		if p.Check != "" {
			fmt.Fprintf(&b, "**Check:** %s\n", p.Check)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// AssembleReviewPrompt builds a complete review prompt with diff and principles.
func AssembleReviewPrompt(diff string, principles []Principle) string {
	var b strings.Builder
	b.WriteString("You are a code reviewer. Analyze the following diff against the provided principles.\n\n")
	b.WriteString("For each finding, output structured JSON with:\n")
	b.WriteString("- file: the file path\n")
	b.WriteString("- line: the line number\n")
	b.WriteString("- principle_id: which principle is violated\n")
	b.WriteString("- severity: critical | warning | info\n")
	b.WriteString("- message: what the issue is\n")
	b.WriteString("- suggestion: how to fix it\n\n")

	b.WriteString(AssemblePrompt(principles))

	b.WriteString("## Diff to Review\n\n```diff\n")
	b.WriteString(diff)
	b.WriteString("\n```\n")

	return b.String()
}
