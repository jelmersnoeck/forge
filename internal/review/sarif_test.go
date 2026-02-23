package review

import (
	"encoding/json"
	"testing"

	"github.com/jelmersnoeck/forge/internal/principles"
)

func TestToSARIF(t *testing.T) {
	findings := []Finding{
		{
			File:        "main.go",
			Line:        10,
			PrincipleID: "sec-001",
			Severity:    principles.SeverityCritical,
			Message:     "SQL injection vulnerability",
			Suggestion:  "Use parameterized queries",
		},
		{
			File:        "handler.go",
			Line:        42,
			PrincipleID: "sec-002",
			Severity:    principles.SeverityWarning,
			Message:     "Missing authentication check",
		},
		{
			File:        "config.go",
			Line:        5,
			PrincipleID: "arch-001",
			Severity:    principles.SeverityInfo,
			Message:     "Consider using environment variables",
		},
	}

	data, err := ToSARIF(findings, "forge-review")
	if err != nil {
		t.Fatalf("ToSARIF() error: %v", err)
	}

	var log SARIFLog
	if err := json.Unmarshal(data, &log); err != nil {
		t.Fatalf("Failed to unmarshal SARIF output: %v", err)
	}

	// Verify version and schema.
	if log.Version != "2.1.0" {
		t.Errorf("Version = %q, want %q", log.Version, "2.1.0")
	}
	if log.Schema != "https://json.schemastore.org/sarif-2.1.0.json" {
		t.Errorf("Schema = %q, want SARIF schema URL", log.Schema)
	}

	// Verify runs.
	if len(log.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(log.Runs))
	}
	run := log.Runs[0]

	// Verify tool name.
	if run.Tool.Driver.Name != "forge-review" {
		t.Errorf("Tool.Driver.Name = %q, want %q", run.Tool.Driver.Name, "forge-review")
	}

	// Verify rules.
	if len(run.Tool.Driver.Rules) != 3 {
		t.Errorf("expected 3 rules, got %d", len(run.Tool.Driver.Rules))
	}

	// Verify results.
	if len(run.Results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(run.Results))
	}
}

func TestToSARIFSeverityMapping(t *testing.T) {
	tests := []struct {
		severity  principles.Severity
		wantLevel string
	}{
		{principles.SeverityCritical, "error"},
		{principles.SeverityWarning, "warning"},
		{principles.SeverityInfo, "note"},
	}

	for _, tt := range tests {
		t.Run(string(tt.severity), func(t *testing.T) {
			findings := []Finding{
				{File: "test.go", Line: 1, PrincipleID: "test-001", Severity: tt.severity, Message: "test"},
			}
			data, err := ToSARIF(findings, "test")
			if err != nil {
				t.Fatalf("ToSARIF() error: %v", err)
			}

			var log SARIFLog
			if err := json.Unmarshal(data, &log); err != nil {
				t.Fatalf("Failed to unmarshal: %v", err)
			}

			if len(log.Runs[0].Results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(log.Runs[0].Results))
			}
			got := log.Runs[0].Results[0].Level
			if got != tt.wantLevel {
				t.Errorf("level = %q, want %q", got, tt.wantLevel)
			}
		})
	}
}

func TestToSARIFEmptyFindings(t *testing.T) {
	data, err := ToSARIF(nil, "forge-review")
	if err != nil {
		t.Fatalf("ToSARIF() error: %v", err)
	}

	var log SARIFLog
	if err := json.Unmarshal(data, &log); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if len(log.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(log.Runs))
	}
	if len(log.Runs[0].Results) != 0 {
		t.Errorf("expected 0 results, got %d", len(log.Runs[0].Results))
	}
}

func TestToSARIFDefaultToolName(t *testing.T) {
	data, err := ToSARIF(nil, "")
	if err != nil {
		t.Fatalf("ToSARIF() error: %v", err)
	}

	var log SARIFLog
	if err := json.Unmarshal(data, &log); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if log.Runs[0].Tool.Driver.Name != "forge-review" {
		t.Errorf("expected default tool name 'forge-review', got %q", log.Runs[0].Tool.Driver.Name)
	}
}

func TestToSARIFLocationWithoutLine(t *testing.T) {
	findings := []Finding{
		{File: "main.go", Line: 0, PrincipleID: "sec-001", Severity: principles.SeverityCritical, Message: "file-level issue"},
	}

	data, err := ToSARIF(findings, "test")
	if err != nil {
		t.Fatalf("ToSARIF() error: %v", err)
	}

	var log SARIFLog
	if err := json.Unmarshal(data, &log); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	result := log.Runs[0].Results[0]
	if len(result.Locations) != 1 {
		t.Fatalf("expected 1 location, got %d", len(result.Locations))
	}
	if result.Locations[0].PhysicalLocation.Region != nil {
		t.Error("expected nil region when line is 0")
	}
}

func TestToSARIFMessageWithSuggestion(t *testing.T) {
	findings := []Finding{
		{File: "main.go", Line: 1, PrincipleID: "sec-001", Severity: principles.SeverityCritical, Message: "issue found", Suggestion: "fix it"},
	}

	data, err := ToSARIF(findings, "test")
	if err != nil {
		t.Fatalf("ToSARIF() error: %v", err)
	}

	var log SARIFLog
	if err := json.Unmarshal(data, &log); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	msg := log.Runs[0].Results[0].Message.Text
	if msg != "issue found\n\nSuggestion: fix it" {
		t.Errorf("message = %q, want message with suggestion", msg)
	}
}

func TestToSARIFRuleDeduplication(t *testing.T) {
	// Two findings with the same principle should produce one rule.
	findings := []Finding{
		{File: "a.go", Line: 1, PrincipleID: "sec-001", Severity: principles.SeverityCritical, Message: "issue1"},
		{File: "b.go", Line: 2, PrincipleID: "sec-001", Severity: principles.SeverityCritical, Message: "issue2"},
	}

	data, err := ToSARIF(findings, "test")
	if err != nil {
		t.Fatalf("ToSARIF() error: %v", err)
	}

	var log SARIFLog
	if err := json.Unmarshal(data, &log); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if len(log.Runs[0].Tool.Driver.Rules) != 1 {
		t.Errorf("expected 1 rule (deduplicated), got %d", len(log.Runs[0].Tool.Driver.Rules))
	}
	if len(log.Runs[0].Results) != 2 {
		t.Errorf("expected 2 results, got %d", len(log.Runs[0].Results))
	}
}

func TestToSARIFValidJSON(t *testing.T) {
	findings := []Finding{
		{File: "main.go", Line: 10, PrincipleID: "sec-001", Severity: principles.SeverityCritical, Message: "test with \"quotes\" and\nnewlines"},
	}

	data, err := ToSARIF(findings, "test")
	if err != nil {
		t.Fatalf("ToSARIF() error: %v", err)
	}

	// Verify it's valid JSON by round-tripping.
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Errorf("SARIF output is not valid JSON: %v", err)
	}
}
