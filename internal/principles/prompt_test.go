package principles

import (
	"strings"
	"testing"
)

func TestAssemblePrompt_VariousPrinciples(t *testing.T) {
	principles := []Principle{
		{
			ID:          "sec-001",
			Category:    CategorySecurity,
			Severity:    SeverityCritical,
			Title:       "No hardcoded secrets",
			Description: "Never embed secrets in code.",
			Rationale:   "Secrets in code end up in version control.",
			Check:       "Scan for string literals that look like keys.",
		},
		{
			ID:          "arch-001",
			Category:    CategoryArchitecture,
			Severity:    SeverityWarning,
			Title:       "Interface-first design",
			Description: "Define interfaces before implementations.",
			Rationale:   "Enables testability.",
			Check:       "Verify key abstractions are interfaces.",
		},
	}

	output := AssemblePrompt(principles)

	// Verify the header is present.
	if !strings.Contains(output, "## Principles") {
		t.Error("output missing '## Principles' header")
	}
	if !strings.Contains(output, "You MUST evaluate") {
		t.Error("output missing evaluation instruction")
	}

	// Verify each principle's ID appears in the output.
	for _, p := range principles {
		if !strings.Contains(output, p.ID) {
			t.Errorf("output missing principle ID %q", p.ID)
		}
		if !strings.Contains(output, p.Title) {
			t.Errorf("output missing principle title %q", p.Title)
		}
		if !strings.Contains(output, string(p.Severity)) {
			t.Errorf("output missing severity %q for %s", p.Severity, p.ID)
		}
		if !strings.Contains(output, string(p.Category)) {
			t.Errorf("output missing category %q for %s", p.Category, p.ID)
		}
		if !strings.Contains(output, p.Description) {
			t.Errorf("output missing description for %s", p.ID)
		}
		if !strings.Contains(output, p.Rationale) {
			t.Errorf("output missing rationale for %s", p.ID)
		}
		if !strings.Contains(output, p.Check) {
			t.Errorf("output missing check for %s", p.ID)
		}
	}

	// Verify format: [ID] Title (severity: ...) pattern.
	if !strings.Contains(output, "[sec-001] No hardcoded secrets (severity: critical)") {
		t.Error("output missing formatted principle header for sec-001")
	}
	if !strings.Contains(output, "[arch-001] Interface-first design (severity: warning)") {
		t.Error("output missing formatted principle header for arch-001")
	}

	// Verify category labels.
	if !strings.Contains(output, "**Category:** security") {
		t.Error("output missing category label for security")
	}
	if !strings.Contains(output, "**Category:** architecture") {
		t.Error("output missing category label for architecture")
	}
}

func TestAssemblePrompt_EmptyList(t *testing.T) {
	output := AssemblePrompt(nil)
	if output != "" {
		t.Errorf("expected empty string for nil principles, got %q", output)
	}

	output = AssemblePrompt([]Principle{})
	if output != "" {
		t.Errorf("expected empty string for empty principles, got %q", output)
	}
}

func TestAssemblePrompt_NoRationaleOrCheck(t *testing.T) {
	principles := []Principle{
		{
			ID:          "sim-001",
			Category:    CategorySimplicity,
			Severity:    SeverityInfo,
			Title:       "Keep it simple",
			Description: "Simple code only.",
		},
	}

	output := AssemblePrompt(principles)

	// Should not contain Rationale or Check labels when empty.
	if strings.Contains(output, "**Rationale:**") {
		t.Error("output should not include Rationale label when rationale is empty")
	}
	if strings.Contains(output, "**Check:**") {
		t.Error("output should not include Check label when check is empty")
	}
}

func TestAssembleReviewPrompt_IncludesDiffAndPrinciples(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,5 @@
 package main
+
+const secret = "hunter2"
`
	principles := []Principle{
		{
			ID:          "sec-001",
			Category:    CategorySecurity,
			Severity:    SeverityCritical,
			Title:       "No hardcoded secrets",
			Description: "Never embed secrets in code.",
			Rationale:   "Secrets in code end up in version control.",
			Check:       "Scan for string literals that look like keys.",
		},
	}

	output := AssembleReviewPrompt(diff, principles)

	// Review prompt header.
	if !strings.Contains(output, "You are a code reviewer") {
		t.Error("output missing review prompt header")
	}

	// Structured JSON output instructions.
	if !strings.Contains(output, "principle_id") {
		t.Error("output missing structured JSON field 'principle_id'")
	}
	if !strings.Contains(output, "severity") {
		t.Error("output missing structured JSON field 'severity'")
	}

	// Principles section.
	if !strings.Contains(output, "## Principles") {
		t.Error("output missing principles section")
	}
	if !strings.Contains(output, "sec-001") {
		t.Error("output missing principle ID sec-001")
	}

	// Diff section.
	if !strings.Contains(output, "## Diff to Review") {
		t.Error("output missing diff section header")
	}
	if !strings.Contains(output, "const secret") {
		t.Error("output missing diff content")
	}
	if !strings.Contains(output, "```diff") {
		t.Error("output missing diff code fence")
	}
}

func TestAssembleReviewPrompt_EmptyPrinciples(t *testing.T) {
	diff := "some diff"
	output := AssembleReviewPrompt(diff, nil)

	// Should still include the diff even with no principles.
	if !strings.Contains(output, diff) {
		t.Error("output missing diff content when principles are empty")
	}
	if !strings.Contains(output, "## Diff to Review") {
		t.Error("output missing diff section header when principles are empty")
	}
}

func TestAssemblePrompt_OutputFormat(t *testing.T) {
	principles := []Principle{
		{
			ID:          "test-001",
			Category:    CategoryTesting,
			Severity:    SeverityWarning,
			Title:       "Table-driven tests",
			Description: "Use table-driven tests.",
			Rationale:   "They reduce duplication.",
			Check:       "Look for repeated test patterns.",
		},
	}

	output := AssemblePrompt(principles)

	// Verify the markdown structure.
	lines := strings.Split(output, "\n")
	foundH2 := false
	foundH3 := false
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			foundH2 = true
		}
		if strings.HasPrefix(line, "### ") {
			foundH3 = true
		}
	}
	if !foundH2 {
		t.Error("output missing H2 heading")
	}
	if !foundH3 {
		t.Error("output missing H3 heading for individual principle")
	}
}
