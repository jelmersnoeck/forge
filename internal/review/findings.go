// Package review handles review orchestration, findings management,
// and deduplication for the governed build loop.
package review

import (
	"github.com/jelmersnoeck/forge/internal/principles"
)

// Finding represents a single review issue found by an agent.
type Finding struct {
	File        string              `json:"file"`
	Line        int                 `json:"line"`
	PrincipleID string              `json:"principle_id"`
	Severity    principles.Severity `json:"severity"`
	Message     string              `json:"message"`
	Suggestion  string              `json:"suggestion"`
	Reviewer    string              `json:"reviewer"` // Which reviewer found this.
}

// Result aggregates findings from one or more review agents.
type Result struct {
	Findings          []Finding `json:"findings"`
	HasCritical       bool      `json:"has_critical"`
	PrinciplesCovered []string  `json:"principles_covered"`
}

// HasCriticalFindings returns true if any finding has critical severity.
func HasCriticalFindings(findings []Finding) bool {
	for _, f := range findings {
		if f.Severity == principles.SeverityCritical {
			return true
		}
	}
	return false
}

// Deduplicate removes duplicate findings based on file+line+principle_id.
func Deduplicate(findings []Finding) []Finding {
	type key struct {
		File        string
		Line        int
		PrincipleID string
	}
	seen := make(map[key]bool)
	var result []Finding
	for _, f := range findings {
		k := key{File: f.File, Line: f.Line, PrincipleID: f.PrincipleID}
		if !seen[k] {
			seen[k] = true
			result = append(result, f)
		}
	}
	return result
}

// FilterBySeverity returns findings at or above the given severity threshold.
func FilterBySeverity(findings []Finding, threshold principles.Severity) []Finding {
	order := map[principles.Severity]int{
		principles.SeverityInfo:     0,
		principles.SeverityWarning:  1,
		principles.SeverityCritical: 2,
	}
	minLevel := order[threshold]
	var result []Finding
	for _, f := range findings {
		if order[f.Severity] >= minLevel {
			result = append(result, f)
		}
	}
	return result
}

// CoveredPrinciples extracts the unique set of principle IDs from findings.
func CoveredPrinciples(findings []Finding) []string {
	seen := make(map[string]bool)
	var ids []string
	for _, f := range findings {
		if f.PrincipleID != "" && !seen[f.PrincipleID] {
			seen[f.PrincipleID] = true
			ids = append(ids, f.PrincipleID)
		}
	}
	return ids
}
