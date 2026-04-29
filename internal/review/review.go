// Package review implements parallel multi-reviewer code review via LLM providers.
package review

// Severity classifies the importance of a review finding.
type Severity string

const (
	SeverityCritical   Severity = "critical"
	SeverityWarning    Severity = "warning"
	SeveritySuggestion Severity = "suggestion"
)

// Finding is a single review observation tied to a file location.
type Finding struct {
	Reviewer    string   `json:"reviewer"`
	Provider    string   `json:"provider"`
	Severity    Severity `json:"severity"`
	File        string   `json:"file,omitempty"`
	StartLine   int      `json:"startLine,omitempty"`
	EndLine     int      `json:"endLine,omitempty"`
	Description string   `json:"description"`
}

// ReviewResult captures findings (or an error) from one reviewer×provider run.
type ReviewResult struct {
	Reviewer string    `json:"reviewer"`
	Provider string    `json:"provider"`
	Findings []Finding `json:"findings"`
	Error    string    `json:"error,omitempty"`
}

// ConsolidatedFinding is a deduplicated finding with source attribution.
type ConsolidatedFinding struct {
	Severity    Severity `json:"severity"`
	File        string   `json:"file,omitempty"`
	StartLine   int      `json:"startLine,omitempty"`
	EndLine     int      `json:"endLine,omitempty"`
	Description string   `json:"description"`
	Sources     []Source `json:"sources"`
}

// Source identifies which reviewer+provider flagged this issue.
type Source struct {
	Reviewer string `json:"reviewer"`
	Provider string `json:"provider"`
}

// ConsolidatedResults wraps both raw and deduplicated findings.
type ConsolidatedResults struct {
	Raw          []ReviewResult
	Consolidated []ConsolidatedFinding
}

// Reviewer is a specialized code review persona backed by a system prompt.
type Reviewer interface {
	Name() string
	SystemPrompt() string
}
