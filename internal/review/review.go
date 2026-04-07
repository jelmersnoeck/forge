// Package review implements parallel multi-reviewer code review via LLM providers.
package review

// Severity classifies the importance of a review finding.
type Severity string

const (
	SeverityCritical   Severity = "critical"
	SeverityWarning    Severity = "warning"
	SeveritySuggestion Severity = "suggestion"
	SeverityPraise     Severity = "praise"
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

// Reviewer is a specialized code review persona backed by a system prompt.
type Reviewer interface {
	Name() string
	SystemPrompt() string
}
