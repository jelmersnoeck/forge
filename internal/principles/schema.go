// Package principles manages governance principles — structured YAML rules
// that agents must follow during planning, coding, and review.
package principles

// Severity indicates how critical a principle violation is.
type Severity string

const (
	SeverityCritical Severity = "critical" // Must fix before PR.
	SeverityWarning  Severity = "warning"  // Should fix, may block.
	SeverityInfo     Severity = "info"     // Advisory, won't block.
)

// Category groups principles by domain.
type Category string

const (
	CategorySecurity     Category = "security"
	CategoryArchitecture Category = "architecture"
	CategorySimplicity   Category = "simplicity"
	CategoryTesting      Category = "testing"
	CategoryPerformance  Category = "performance"
)

// Principle is a single governance rule with structured metadata.
type Principle struct {
	ID          string   `yaml:"id"`          // Unique ID, e.g. "sec-001".
	Category    Category `yaml:"category"`    // Domain grouping.
	Severity    Severity `yaml:"severity"`    // How critical violations are.
	Title       string   `yaml:"title"`       // Short human-readable name.
	Description string   `yaml:"description"` // Full explanation.
	Rationale   string   `yaml:"rationale"`   // Why this principle matters.
	Check       string   `yaml:"check"`       // Machine-evaluable check instruction.
	Examples    []Example `yaml:"examples"`   // Good/bad examples.
	Tags        []string `yaml:"tags"`        // Additional filtering tags.
}

// Example shows a good or bad code pattern for a principle.
type Example struct {
	Type        string `yaml:"type"`        // "good" or "bad".
	Code        string `yaml:"code"`        // The code example.
	Explanation string `yaml:"explanation"` // Why it's good or bad.
}

// PrincipleSet is a named collection of principles loaded from a YAML file.
type PrincipleSet struct {
	Name        string      `yaml:"name"`        // Set name, e.g. "security".
	Version     string      `yaml:"version"`     // Semver version.
	Description string      `yaml:"description"` // What this set covers.
	Principles  []Principle `yaml:"principles"`  // The principles in this set.
}
