package review

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// ParseFindings parses agent output into a slice of findings.
// It handles:
// - A JSON array of findings
// - A single JSON object finding
// - Text output containing embedded JSON (array or object)
//
// Returns an error if non-empty output cannot be parsed as findings.
// An empty or whitespace-only input returns nil findings with no error.
func ParseFindings(output string) ([]Finding, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil, nil
	}

	// Try direct JSON parse first (array or object).
	if findings := tryParseJSON(output); findings != nil {
		return findings, nil
	}

	// Try to find JSON embedded in text output.
	if findings := extractEmbeddedJSON(output); findings != nil {
		return findings, nil
	}

	slog.Warn("could not parse any findings from agent output", "output_length", len(output))
	return nil, fmt.Errorf("could not parse review output as findings (output length: %d)", len(output))
}

// tryParseJSON attempts to parse the input as a JSON array of findings
// or a single JSON object finding.
func tryParseJSON(input string) []Finding {
	input = strings.TrimSpace(input)

	// Try as array first.
	var findings []Finding
	if err := json.Unmarshal([]byte(input), &findings); err == nil {
		return findings
	}

	// Try as single object.
	var single Finding
	if err := json.Unmarshal([]byte(input), &single); err == nil {
		if single.File != "" || single.Message != "" || single.PrincipleID != "" {
			return []Finding{single}
		}
	}

	return nil
}

// extractEmbeddedJSON searches for JSON arrays or objects embedded in text.
// Agents sometimes wrap JSON in markdown code fences or explanatory text.
func extractEmbeddedJSON(text string) []Finding {
	// Try to find JSON array in the text.
	if findings := findAndParseJSON(text, '[', ']'); findings != nil {
		return findings
	}

	// Try to find JSON object in the text.
	if findings := findAndParseJSON(text, '{', '}'); findings != nil {
		return findings
	}

	return nil
}

// findAndParseJSON searches for balanced JSON delimited by open/close chars.
func findAndParseJSON(text string, open, close byte) []Finding {
	start := strings.IndexByte(text, open)
	if start == -1 {
		return nil
	}

	// Find the matching closing bracket by counting nesting depth.
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && inString {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if c == open {
			depth++
		} else if c == close {
			depth--
			if depth == 0 {
				candidate := text[start : i+1]
				if findings := tryParseJSON(candidate); findings != nil {
					return findings
				}
				// If this candidate didn't parse, try finding the next one.
				rest := text[i+1:]
				return findAndParseJSON(rest, open, close)
			}
		}
	}
	return nil
}
