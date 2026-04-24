package review

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
)

const (
	consolidationTimeout  = 2 * time.Minute
	maxFindingsForPrompt  = 100
)

// Consolidate deduplicates and calibrates findings from multiple agents.
// Falls back to converting raw findings 1:1 if the LLM call fails.
func Consolidate(
	ctx context.Context,
	provider types.LLMProvider,
	results []ReviewResult,
) ([]ConsolidatedFinding, error) {
	allFindings := collectAllFindings(results)
	if len(allFindings) == 0 {
		return nil, nil
	}

	consolidateCtx, cancel := context.WithTimeout(ctx, consolidationTimeout)
	defer cancel()

	userPrompt := buildConsolidationPrompt(results)

	chatReq := types.ChatRequest{
		Model: "claude-sonnet-4-20250514",
		System: []types.SystemBlock{
			{Type: "text", Text: consolidationSystemPrompt()},
		},
		Messages: []types.ChatMessage{
			{
				Role: "user",
				Content: []types.ChatContentBlock{
					{Type: "text", Text: userPrompt},
				},
			},
		},
		MaxTokens: 4096,
		Stream:    true,
	}

	deltaChan, err := provider.Chat(consolidateCtx, chatReq)
	if err != nil {
		log.Printf("[consolidate] provider error, falling back to raw: %v", err)
		return fallbackToRaw(results), nil
	}

	responseText := collectResponse(consolidateCtx, deltaChan)

	// If context expired before we got a response, fall back.
	if consolidateCtx.Err() != nil && strings.TrimSpace(responseText) == "" {
		log.Printf("[consolidate] timeout, falling back to raw")
		return fallbackToRaw(results), nil
	}

	consolidated, err := parseConsolidationResponse(responseText)
	if err != nil {
		log.Printf("[consolidate] parse error, falling back to raw: %v", err)
		return fallbackToRaw(results), nil
	}

	rawCount := len(allFindings)
	if len(consolidated) > rawCount {
		log.Printf("[consolidate] warning: consolidation produced %d findings from %d raw (LLM may have invented findings)", len(consolidated), rawCount)
	}

	return consolidated, nil
}

// consolidationSystemPrompt returns the system prompt for the review manager.
func consolidationSystemPrompt() string {
	return `You are a senior review manager. You receive code review findings from
multiple independent reviewers and providers. Your job is to:

1. Group findings that describe the same underlying issue (same file/region,
   same root cause) regardless of which reviewer or provider produced them.
2. For each group, produce a single consolidated finding with:
   - A calibrated severity ("critical", "warning", or "suggestion") based on
     consensus across sources. If one source says "critical" but others say
     "warning", evaluate whether "critical" is actually justified based on the
     description — don't just pick the max or majority.
   - A merged description that captures the best explanation from across sources.
   - The file/line range (most specific wins).
   - A "sources" list indicating which reviewer+provider combinations flagged it.
3. Discard findings that appear to be noise/false positives when the majority
   of agents did not flag them AND they are low-severity (suggestion only).

Respond with ONLY a JSON array. No prose, no markdown fences, no explanation.
Each element must have this exact shape:
{
  "severity": "critical" | "warning" | "suggestion",
  "file": "path/to/file.go",
  "startLine": 42,
  "endLine": 50,
  "description": "Clear description of the issue",
  "sources": [{"reviewer": "name", "provider": "name"}, ...]
}

Rules:
- "file", "startLine", "endLine" are optional (omit if not applicable).
- Every finding from the input must either appear in a consolidated group or be
  explicitly discarded as noise. Do not lose legitimate findings.
- Do not invent findings that were not in the input.
- If there is only one finding, you must still return it (possibly with
  recalibrated severity).`
}

// buildConsolidationPrompt constructs the user message listing all findings.
func buildConsolidationPrompt(results []ReviewResult) string {
	type annotatedFinding struct {
		Finding  Finding
		Reviewer string
		Provider string
	}

	var all []annotatedFinding
	for _, r := range results {
		for _, f := range r.Findings {
			all = append(all, annotatedFinding{
				Finding:  f,
				Reviewer: r.Reviewer,
				Provider: r.Provider,
			})
		}
	}

	// Truncate if over the limit, keeping highest severity first.
	if len(all) > maxFindingsForPrompt {
		sort.SliceStable(all, func(i, j int) bool {
			return severityRank(all[i].Finding.Severity) > severityRank(all[j].Finding.Severity)
		})
		all = all[:maxFindingsForPrompt]
	}

	var sb strings.Builder
	sb.WriteString("Here are the raw findings from all reviewers. Consolidate them:\n\n")

	for i, af := range all {
		fmt.Fprintf(&sb, "### Finding %d\n", i+1)
		fmt.Fprintf(&sb, "- Reviewer: %s\n", af.Reviewer)
		fmt.Fprintf(&sb, "- Provider: %s\n", af.Provider)
		fmt.Fprintf(&sb, "- Severity: %s\n", af.Finding.Severity)
		if af.Finding.File != "" {
			fmt.Fprintf(&sb, "- File: %s\n", af.Finding.File)
			if af.Finding.StartLine > 0 {
				fmt.Fprintf(&sb, "- StartLine: %d\n", af.Finding.StartLine)
			}
			if af.Finding.EndLine > 0 {
				fmt.Fprintf(&sb, "- EndLine: %d\n", af.Finding.EndLine)
			}
		}
		fmt.Fprintf(&sb, "- Description: %s\n\n", af.Finding.Description)
	}

	return sb.String()
}

// parseConsolidationResponse extracts consolidated findings from LLM output.
func parseConsolidationResponse(text string) ([]ConsolidatedFinding, error) {
	text = strings.TrimSpace(text)
	if text == "" || text == "[]" {
		return nil, nil
	}

	stripped := stripCodeFences(text)

	var findings []ConsolidatedFinding
	if err := json.Unmarshal([]byte(stripped), &findings); err == nil {
		return findings, nil
	}

	match := jsonArrayRe.FindString(stripped)
	if match == "" {
		match = jsonArrayRe.FindString(text)
	}
	if match == "" {
		return nil, fmt.Errorf("no JSON array found in consolidation response")
	}

	if err := json.Unmarshal([]byte(match), &findings); err != nil {
		return nil, fmt.Errorf("invalid JSON in consolidation response: %w", err)
	}
	return findings, nil
}

// fallbackToRaw converts raw findings 1:1 into ConsolidatedFinding.
func fallbackToRaw(results []ReviewResult) []ConsolidatedFinding {
	var out []ConsolidatedFinding
	for _, r := range results {
		for _, f := range r.Findings {
			out = append(out, ConsolidatedFinding{
				Severity:    f.Severity,
				File:        f.File,
				StartLine:   f.StartLine,
				EndLine:     f.EndLine,
				Description: f.Description,
				Sources: []Source{{
					Reviewer: r.Reviewer,
					Provider: r.Provider,
				}},
			})
		}
	}
	return out
}

// collectAllFindings gathers all findings from all results, ignoring errors.
func collectAllFindings(results []ReviewResult) []Finding {
	var all []Finding
	for _, r := range results {
		all = append(all, r.Findings...)
	}
	return all
}

// severityRank returns a numeric rank for sorting (higher = more severe).
func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	case SeveritySuggestion:
		return 1
	default:
		return 0
	}
}

// FormatConsolidatedMessage builds a human-readable message listing consolidated
// findings with source attribution.
func FormatConsolidatedMessage(findings []ConsolidatedFinding) string {
	var sb strings.Builder
	sb.WriteString("The code review found the following issues. Please fix them:\n")

	for _, f := range findings {
		loc := ""
		if f.File != "" {
			loc = f.File
			if f.StartLine > 0 {
				loc += fmt.Sprintf(":%d", f.StartLine)
			}
			loc += " — "
		}

		var sources []string
		for _, s := range f.Sources {
			sources = append(sources, s.Reviewer+"/"+s.Provider)
		}

		fmt.Fprintf(&sb, "\n- [%s] %s%s (flagged by: %s)",
			f.Severity, loc, f.Description, strings.Join(sources, ", "))
	}

	return sb.String()
}

// FormatConsolidatedForCoder converts consolidated findings into a prompt for
// the coder. Only critical and warning findings are included.
func FormatConsolidatedForCoder(findings []ConsolidatedFinding) string {
	var sb strings.Builder
	sb.WriteString("Fix ONLY the issues listed below. Do not refactor unrelated code or address issues not in this list.\n")

	for _, f := range findings {
		switch f.Severity {
		case SeverityCritical, SeverityWarning:
			// include
		default:
			continue
		}
		loc := ""
		if f.File != "" {
			loc = f.File
			if f.StartLine > 0 {
				loc += fmt.Sprintf(":%d", f.StartLine)
			}
			loc += " — "
		}
		fmt.Fprintf(&sb, "\n- [%s] %s%s", f.Severity, loc, f.Description)
	}

	return sb.String()
}

// HasConsolidatedHighSeverity returns true if any consolidated finding is
// critical or warning.
func HasConsolidatedHighSeverity(findings []ConsolidatedFinding) bool {
	for _, f := range findings {
		switch f.Severity {
		case SeverityCritical, SeverityWarning:
			return true
		}
	}
	return false
}

// HasConsolidatedCritical returns true if any consolidated finding is critical.
func HasConsolidatedCritical(findings []ConsolidatedFinding) bool {
	for _, f := range findings {
		if f.Severity == SeverityCritical {
			return true
		}
	}
	return false
}
