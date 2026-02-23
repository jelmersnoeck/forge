package review

import (
	"encoding/json"
	"fmt"

	"github.com/jelmersnoeck/forge/internal/principles"
)

// SARIF v2.1.0 types for GitHub Code Scanning integration.
// See: https://docs.oasis-open.org/sarif/sarif/v2.1.0/sarif-v2.1.0.html

// SARIFLog is the top-level SARIF object.
type SARIFLog struct {
	Version string     `json:"version"`
	Schema  string     `json:"$schema"`
	Runs    []SARIFRun `json:"runs"`
}

// SARIFRun represents a single analysis run.
type SARIFRun struct {
	Tool    SARIFTool     `json:"tool"`
	Results []SARIFResult `json:"results"`
}

// SARIFTool describes the analysis tool.
type SARIFTool struct {
	Driver SARIFDriver `json:"driver"`
}

// SARIFDriver contains tool metadata and rule definitions.
type SARIFDriver struct {
	Name           string      `json:"name"`
	InformationURI string      `json:"informationUri,omitempty"`
	Rules          []SARIFRule `json:"rules,omitempty"`
}

// SARIFRule defines a rule (mapped from a principle).
type SARIFRule struct {
	ID               string               `json:"id"`
	ShortDescription SARIFMessage         `json:"shortDescription"`
	HelpURI          string               `json:"helpUri,omitempty"`
	Properties       *SARIFRuleProperties `json:"properties,omitempty"`
}

// SARIFRuleProperties contains additional rule metadata.
type SARIFRuleProperties struct {
	Tags []string `json:"tags,omitempty"`
}

// SARIFResult is a single finding in SARIF format.
type SARIFResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   SARIFMessage    `json:"message"`
	Locations []SARIFLocation `json:"locations,omitempty"`
}

// SARIFMessage contains human-readable text.
type SARIFMessage struct {
	Text string `json:"text"`
}

// SARIFLocation represents a code location.
type SARIFLocation struct {
	PhysicalLocation SARIFPhysicalLocation `json:"physicalLocation"`
}

// SARIFPhysicalLocation describes a file and position.
type SARIFPhysicalLocation struct {
	ArtifactLocation SARIFArtifactLocation `json:"artifactLocation"`
	Region           *SARIFRegion          `json:"region,omitempty"`
}

// SARIFArtifactLocation identifies a file.
type SARIFArtifactLocation struct {
	URI string `json:"uri"`
}

// SARIFRegion identifies a position within a file.
type SARIFRegion struct {
	StartLine int `json:"startLine"`
}

// ToSARIF converts a slice of findings into SARIF v2.1.0 JSON format.
func ToSARIF(findings []Finding, toolName string) ([]byte, error) {
	if toolName == "" {
		toolName = "forge-review"
	}

	// Build rule definitions from unique principle IDs.
	ruleMap := make(map[string]SARIFRule)
	for _, f := range findings {
		if f.PrincipleID == "" {
			continue
		}
		if _, exists := ruleMap[f.PrincipleID]; !exists {
			ruleMap[f.PrincipleID] = SARIFRule{
				ID:               f.PrincipleID,
				ShortDescription: SARIFMessage{Text: fmt.Sprintf("Principle %s violation", f.PrincipleID)},
			}
		}
	}

	rules := make([]SARIFRule, 0, len(ruleMap))
	for _, r := range ruleMap {
		rules = append(rules, r)
	}

	// Build results.
	results := make([]SARIFResult, 0, len(findings))
	for _, f := range findings {
		result := SARIFResult{
			RuleID:  f.PrincipleID,
			Level:   severityToSARIFLevel(f.Severity),
			Message: SARIFMessage{Text: buildSARIFMessage(f)},
		}

		if f.File != "" {
			loc := SARIFLocation{
				PhysicalLocation: SARIFPhysicalLocation{
					ArtifactLocation: SARIFArtifactLocation{URI: f.File},
				},
			}
			if f.Line > 0 {
				loc.PhysicalLocation.Region = &SARIFRegion{StartLine: f.Line}
			}
			result.Locations = []SARIFLocation{loc}
		}

		results = append(results, result)
	}

	log := SARIFLog{
		Version: "2.1.0",
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Runs: []SARIFRun{
			{
				Tool: SARIFTool{
					Driver: SARIFDriver{
						Name:  toolName,
						Rules: rules,
					},
				},
				Results: results,
			},
		},
	}

	data, err := json.MarshalIndent(log, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling SARIF: %w", err)
	}
	return data, nil
}

// severityToSARIFLevel maps principle severity to SARIF level.
func severityToSARIFLevel(s principles.Severity) string {
	switch s {
	case principles.SeverityCritical:
		return "error"
	case principles.SeverityWarning:
		return "warning"
	case principles.SeverityInfo:
		return "note"
	default:
		return "note"
	}
}

// buildSARIFMessage constructs a human-readable message from a finding.
func buildSARIFMessage(f Finding) string {
	msg := f.Message
	if f.Suggestion != "" {
		msg += "\n\nSuggestion: " + f.Suggestion
	}
	return msg
}
