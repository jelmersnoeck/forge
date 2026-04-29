package review

import (
	"strings"
	"unicode"
)

// dedupFindings groups raw findings that describe the same issue
// (same file, overlapping lines, similar description) and merges
// them into ConsolidatedFinding entries with source attribution.
//
// This is a deterministic fallback — no LLM needed. It runs in O(n²)
// which is fine for the expected ≤100 findings per review.
func dedupFindings(results []ReviewResult) []ConsolidatedFinding {
	type annotated struct {
		finding  Finding
		reviewer string
		provider string
	}

	var all []annotated
	for _, r := range results {
		for _, f := range r.Findings {
			all = append(all, annotated{
				finding:  f,
				reviewer: r.Reviewer,
				provider: r.Provider,
			})
		}
	}

	if len(all) == 0 {
		return nil
	}

	// grouped[i] == index of the group representative for all[i].
	// -1 means "is a group representative" (or not yet grouped).
	grouped := make([]int, len(all))
	for i := range grouped {
		grouped[i] = -1
	}

	for i := 0; i < len(all); i++ {
		if grouped[i] >= 0 {
			continue // already merged into another group
		}
		for j := i + 1; j < len(all); j++ {
			if grouped[j] >= 0 {
				continue
			}
			if isSameFinding(all[i].finding, all[j].finding) {
				grouped[j] = i
			}
		}
	}

	// Build consolidated findings from groups.
	var out []ConsolidatedFinding
	for i := 0; i < len(all); i++ {
		if grouped[i] >= 0 {
			continue // not a representative
		}

		cf := ConsolidatedFinding{
			Severity:    all[i].finding.Severity,
			File:        all[i].finding.File,
			StartLine:   all[i].finding.StartLine,
			EndLine:     all[i].finding.EndLine,
			Description: all[i].finding.Description,
			Sources: []Source{{
				Reviewer: all[i].reviewer,
				Provider: all[i].provider,
			}},
		}

		for j := i + 1; j < len(all); j++ {
			if grouped[j] != i {
				continue
			}
			cf.Sources = append(cf.Sources, Source{
				Reviewer: all[j].reviewer,
				Provider: all[j].provider,
			})

			// Promote severity: if any member is more severe, upgrade.
			if severityRank(all[j].finding.Severity) > severityRank(cf.Severity) {
				cf.Severity = all[j].finding.Severity
			}

			// Keep the longer (more specific) description.
			if len(all[j].finding.Description) > len(cf.Description) {
				cf.Description = all[j].finding.Description
			}

			// Widen the line range.
			cf.StartLine, cf.EndLine = mergeLineRange(
				cf.StartLine, cf.EndLine,
				all[j].finding.StartLine, all[j].finding.EndLine,
			)
		}

		out = append(out, cf)
	}

	return out
}

// dedupConsolidated merges ConsolidatedFinding entries that describe
// the same issue. Used as a post-processing step on LLM output.
func dedupConsolidated(findings []ConsolidatedFinding) []ConsolidatedFinding {
	if len(findings) <= 1 {
		return findings
	}

	grouped := make([]int, len(findings))
	for i := range grouped {
		grouped[i] = -1
	}

	for i := 0; i < len(findings); i++ {
		if grouped[i] >= 0 {
			continue
		}
		for j := i + 1; j < len(findings); j++ {
			if grouped[j] >= 0 {
				continue
			}
			fi := Finding{
				Severity:    findings[i].Severity,
				File:        findings[i].File,
				StartLine:   findings[i].StartLine,
				EndLine:     findings[i].EndLine,
				Description: findings[i].Description,
			}
			fj := Finding{
				Severity:    findings[j].Severity,
				File:        findings[j].File,
				StartLine:   findings[j].StartLine,
				EndLine:     findings[j].EndLine,
				Description: findings[j].Description,
			}
			if isSameFinding(fi, fj) {
				grouped[j] = i
			}
		}
	}

	var out []ConsolidatedFinding
	for i := 0; i < len(findings); i++ {
		if grouped[i] >= 0 {
			continue
		}

		cf := findings[i]
		// Deep copy sources to avoid mutating input.
		cf.Sources = append([]Source(nil), cf.Sources...)

		for j := i + 1; j < len(findings); j++ {
			if grouped[j] != i {
				continue
			}

			cf.Sources = append(cf.Sources, findings[j].Sources...)

			if severityRank(findings[j].Severity) > severityRank(cf.Severity) {
				cf.Severity = findings[j].Severity
			}
			if len(findings[j].Description) > len(cf.Description) {
				cf.Description = findings[j].Description
			}
			cf.StartLine, cf.EndLine = mergeLineRange(
				cf.StartLine, cf.EndLine,
				findings[j].StartLine, findings[j].EndLine,
			)
		}

		out = append(out, cf)
	}

	return out
}

// isSameFinding returns true if two findings describe the same issue.
//
// When two findings target the same file with overlapping lines, they almost
// certainly describe the same underlying issue — reviewers just phrase it
// differently. The description threshold is tiered:
//
//   - Same file + overlapping lines → 30% token overlap (lenient)
//   - Same file + no line info      → 40% token overlap (moderate)
//   - No file on either             → 40% token overlap (moderate)
func isSameFinding(a, b Finding) bool {
	// Different files → different findings. Exception: both have no file.
	if a.File != b.File {
		return false
	}

	linesColocated := false
	if a.StartLine > 0 && b.StartLine > 0 {
		if !linesOverlap(a.StartLine, a.EndLine, b.StartLine, b.EndLine) {
			return false
		}
		linesColocated = true
	}

	// Findings at the same location are very likely the same issue even when
	// different reviewers use different vocabulary.
	switch {
	case linesColocated:
		return descriptionSimilarAt(a.Description, b.Description, 0.30)
	default:
		return descriptionSimilarAt(a.Description, b.Description, 0.40)
	}
}

// linesOverlap returns true if two line ranges overlap or are within
// 5 lines of each other (adjacent findings about the same issue).
const lineProximity = 5

func linesOverlap(startA, endA, startB, endB int) bool {
	// Normalize: if endLine is 0, treat it as same as startLine.
	if endA == 0 {
		endA = startA
	}
	if endB == 0 {
		endB = startB
	}

	// Check if ranges overlap or are within proximity.
	return startA <= endB+lineProximity && startB <= endA+lineProximity
}

// descriptionSimilar returns true if two descriptions share enough token
// overlap to be considered the same issue. Delegates to descriptionSimilarAt
// with a 40% threshold.
func descriptionSimilar(a, b string) bool {
	return descriptionSimilarAt(a, b, 0.40)
}

// descriptionSimilarAt returns true if two descriptions share ≥threshold of
// their significant tokens (lowercased, punctuation stripped).
// Requires at least 2 significant tokens in each description for fuzzy
// matching — shorter descriptions need exact equality to avoid false
// positives like "Finding A" ≈ "Finding B".
func descriptionSimilarAt(a, b string, threshold float64) bool {
	tokensA := significantTokens(a)
	tokensB := significantTokens(b)

	if len(tokensA) == 0 || len(tokensB) == 0 {
		return a == b
	}

	// Too few tokens for reliable fuzzy matching — require exact match.
	if len(tokensA) < 2 || len(tokensB) < 2 {
		return strings.EqualFold(
			strings.TrimSpace(a),
			strings.TrimSpace(b),
		)
	}

	setB := make(map[string]bool, len(tokensB))
	for _, t := range tokensB {
		setB[t] = true
	}

	overlap := 0
	for _, t := range tokensA {
		if setB[t] {
			overlap++
		}
	}

	// Use the smaller set as denominator — if ≥threshold of the smaller
	// set's tokens appear in the larger, they're talking about the same thing.
	minLen := len(tokensA)
	if len(tokensB) < minLen {
		minLen = len(tokensB)
	}

	return float64(overlap)/float64(minLen) >= threshold
}

// significantTokens splits text into lowercase tokens, filtering out
// very short words (≤2 chars) and common stop words.
func significantTokens(s string) []string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	})

	var tokens []string
	for _, w := range words {
		if len(w) <= 2 || stopWords[w] {
			continue
		}
		tokens = append(tokens, w)
	}

	return unique(tokens)
}

func unique(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true,
	"but": true, "not": true, "you": true, "all": true,
	"can": true, "has": true, "was": true, "one": true,
	"our": true, "out": true, "its": true, "this": true,
	"that": true, "with": true, "have": true, "from": true,
	"they": true, "been": true, "said": true, "each": true,
	"which": true, "their": true, "will": true, "when": true,
	"who": true, "may": true, "more": true, "than": true,
	"should": true, "could": true, "would": true,
}

// mergeLineRange returns the union of two line ranges.
func mergeLineRange(startA, endA, startB, endB int) (int, int) {
	start := startA
	if startB > 0 && (start == 0 || startB < start) {
		start = startB
	}

	end := endA
	if endB > end {
		end = endB
	}

	return start, end
}
