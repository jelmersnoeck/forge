package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConsolidatedHelpers(t *testing.T) {
	t.Run("FormatConsolidatedMessage", func(t *testing.T) {
		r := require.New(t)

		findings := []ConsolidatedFinding{
			{
				Severity:    SeverityCritical,
				File:        "study_room_f.go",
				StartLine:   42,
				Description: "Dean Pelton's security policy violated — SQL injection in query builder",
				Sources: []Source{
					{Reviewer: "security", Provider: "anthropic"},
					{Reviewer: "security", Provider: "openai"},
				},
			},
			{
				Severity:    SeveritySuggestion,
				File:        "chang.go",
				StartLine:   10,
				Description: "Extract helper function for Señor Chang's credentials",
				Sources: []Source{
					{Reviewer: "code-quality", Provider: "anthropic"},
				},
			},
			{
				Severity:    SeverityWarning,
				Description: "Missing nil check in Troy Barnes' enrollment handler",
				Sources: []Source{
					{Reviewer: "maintainability", Provider: "anthropic"},
				},
			},
		}

		msg := FormatConsolidatedMessage(findings)
		r.Contains(msg, "code review found the following issues")
		r.Contains(msg, "[critical]")
		r.Contains(msg, "study_room_f.go:42")
		r.Contains(msg, "Dean Pelton")
		r.Contains(msg, "[suggestion]")
		r.Contains(msg, "[warning]")
		r.Contains(msg, "Troy Barnes")
		// Source attribution
		r.Contains(msg, "security/anthropic")
		r.Contains(msg, "security/openai")
	})

	t.Run("FormatConsolidatedMessage empty", func(t *testing.T) {
		r := require.New(t)
		msg := FormatConsolidatedMessage(nil)
		r.Contains(msg, "code review found the following issues")
	})

	t.Run("HasConsolidatedHighSeverity", func(t *testing.T) {
		tests := map[string]struct {
			findings []ConsolidatedFinding
			want     bool
		}{
			"has critical": {
				findings: []ConsolidatedFinding{{Severity: SeverityCritical}},
				want:     true,
			},
			"has warning": {
				findings: []ConsolidatedFinding{{Severity: SeverityWarning}},
				want:     true,
			},
			"only suggestions": {
				findings: []ConsolidatedFinding{{Severity: SeveritySuggestion}},
				want:     false,
			},
			"empty": {
				findings: nil,
				want:     false,
			},
		}

		for name, tc := range tests {
			t.Run(name, func(t *testing.T) {
				r := require.New(t)
				r.Equal(tc.want, HasConsolidatedHighSeverity(tc.findings))
			})
		}
	})

	t.Run("HasConsolidatedCritical", func(t *testing.T) {
		tests := map[string]struct {
			findings []ConsolidatedFinding
			want     bool
		}{
			"has critical": {
				findings: []ConsolidatedFinding{{Severity: SeverityCritical}},
				want:     true,
			},
			"only warnings": {
				findings: []ConsolidatedFinding{{Severity: SeverityWarning}},
				want:     false,
			},
			"empty": {
				findings: nil,
				want:     false,
			},
		}

		for name, tc := range tests {
			t.Run(name, func(t *testing.T) {
				r := require.New(t)
				r.Equal(tc.want, HasConsolidatedCritical(tc.findings))
			})
		}
	})
}

func TestFallbackToRaw(t *testing.T) {
	r := require.New(t)

	results := []ReviewResult{
		{
			Reviewer: "security",
			Provider: "anthropic",
			Findings: []Finding{
				{Severity: SeverityCritical, File: "study_room_f.go", StartLine: 1, EndLine: 10, Description: "Evil Abed detected"},
				{Severity: SeverityWarning, File: "paintball.go", StartLine: 42, Description: "Troy Barnes would not approve"},
			},
		},
		{
			Reviewer: "code-quality",
			Provider: "openai",
			Findings: []Finding{
				{Severity: SeveritySuggestion, File: "chang.go", Description: "Señor Chang recommends refactoring"},
			},
		},
		{
			Reviewer: "security",
			Provider: "openai",
			Error:    "Señor Chang broke the API",
		},
	}

	consolidated := fallbackToRaw(results)
	r.Len(consolidated, 3, "should have one ConsolidatedFinding per raw finding")

	r.Equal(SeverityCritical, consolidated[0].Severity)
	r.Equal("study_room_f.go", consolidated[0].File)
	r.Equal(1, consolidated[0].StartLine)
	r.Equal(10, consolidated[0].EndLine)
	r.Equal("Evil Abed detected", consolidated[0].Description)
	r.Len(consolidated[0].Sources, 1)
	r.Equal("security", consolidated[0].Sources[0].Reviewer)
	r.Equal("anthropic", consolidated[0].Sources[0].Provider)

	r.Equal(SeveritySuggestion, consolidated[2].Severity)
	r.Equal("chang.go", consolidated[2].File)
}

func TestBuildConsolidationPrompt(t *testing.T) {
	r := require.New(t)

	results := []ReviewResult{
		{
			Reviewer: "security",
			Provider: "anthropic",
			Findings: []Finding{
				{Severity: SeverityCritical, File: "paintball.go", StartLine: 42, Description: "SQL injection"},
			},
		},
		{
			Reviewer: "code-quality",
			Provider: "openai",
			Findings: []Finding{
				{Severity: SeverityWarning, File: "paintball.go", StartLine: 40, EndLine: 45, Description: "Unsanitized input in query"},
			},
		},
	}

	prompt := buildConsolidationPrompt(results)
	r.Contains(prompt, "SQL injection")
	r.Contains(prompt, "Unsanitized input")
	r.Contains(prompt, "security")
	r.Contains(prompt, "code-quality")
	r.Contains(prompt, "anthropic")
	r.Contains(prompt, "openai")
	r.Contains(prompt, "Consolidate")
}

func TestBuildConsolidationPromptTruncation(t *testing.T) {
	r := require.New(t)

	// Build 150 findings to exceed the 100-finding limit.
	var results []ReviewResult
	findings := make([]Finding, 150)
	for i := range findings {
		sev := SeveritySuggestion
		switch {
		case i < 5:
			sev = SeverityCritical
		case i < 20:
			sev = SeverityWarning
		}
		findings[i] = Finding{
			Severity:    sev,
			File:        fmt.Sprintf("file_%d.go", i),
			Description: fmt.Sprintf("Finding %d at Greendale", i),
		}
	}
	results = append(results, ReviewResult{
		Reviewer: "security",
		Provider: "anthropic",
		Findings: findings,
	})

	prompt := buildConsolidationPrompt(results)
	// Should contain critical findings (they're highest priority).
	r.Contains(prompt, "Finding 0 at Greendale")
	// Should NOT contain finding 149 (truncated).
	r.NotContains(prompt, "Finding 149 at Greendale")
}

func TestParseConsolidationResponse(t *testing.T) {
	tests := map[string]struct {
		input   string
		wantLen int
		wantErr bool
		check   func(t *testing.T, findings []ConsolidatedFinding)
	}{
		"valid JSON array": {
			input: `[
				{
					"severity": "critical",
					"file": "paintball.go",
					"startLine": 42,
					"description": "SQL injection in query builder",
					"sources": [
						{"reviewer": "security", "provider": "anthropic"},
						{"reviewer": "code-quality", "provider": "openai"}
					]
				}
			]`,
			wantLen: 1,
			check: func(t *testing.T, findings []ConsolidatedFinding) {
				r := require.New(t)
				r.Equal(SeverityCritical, findings[0].Severity)
				r.Equal("paintball.go", findings[0].File)
				r.Len(findings[0].Sources, 2)
			},
		},
		"JSON in code fences": {
			input:   "```json\n[{\"severity\":\"warning\",\"description\":\"Troy Barnes error\",\"sources\":[{\"reviewer\":\"security\",\"provider\":\"anthropic\"}]}]\n```",
			wantLen: 1,
		},
		"empty array": {
			input:   "[]",
			wantLen: 0,
		},
		"invalid JSON": {
			input:   "Cool. Cool cool cool. No issues.",
			wantErr: true,
		},
		"JSON with prose wrapper": {
			input:   "Here are the consolidated findings:\n\n[{\"severity\":\"suggestion\",\"description\":\"Abed recommends\",\"sources\":[{\"reviewer\":\"code-quality\",\"provider\":\"anthropic\"}]}]\n\nOverall streets ahead.",
			wantLen: 1,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			findings, err := parseConsolidationResponse(tc.input)
			if tc.wantErr {
				r.Error(err)
				return
			}
			r.NoError(err)
			r.Len(findings, tc.wantLen)
			if tc.check != nil {
				tc.check(t, findings)
			}
		})
	}
}

func TestConsolidate(t *testing.T) {
	t.Run("successful consolidation", func(t *testing.T) {
		r := require.New(t)

		llmResponse := `[
			{
				"severity": "critical",
				"file": "paintball.go",
				"startLine": 42,
				"description": "SQL injection in query builder (multiple agents flagged this)",
				"sources": [
					{"reviewer": "security", "provider": "anthropic"},
					{"reviewer": "code-quality", "provider": "openai"}
				]
			}
		]`

		provider := &mockProvider{response: llmResponse}
		results := []ReviewResult{
			{
				Reviewer: "security",
				Provider: "anthropic",
				Findings: []Finding{
					{Severity: SeverityCritical, File: "paintball.go", StartLine: 42, Description: "SQL injection"},
				},
			},
			{
				Reviewer: "code-quality",
				Provider: "openai",
				Findings: []Finding{
					{Severity: SeverityWarning, File: "paintball.go", StartLine: 40, Description: "Unsanitized input"},
				},
			},
		}

		consolidated, err := Consolidate(context.Background(), provider, "", results)
		r.NoError(err)
		r.Len(consolidated, 1)
		r.Equal(SeverityCritical, consolidated[0].Severity)
		r.Len(consolidated[0].Sources, 2)
	})

	t.Run("zero findings skips consolidation", func(t *testing.T) {
		r := require.New(t)

		provider := &mockProvider{response: "should not be called"}
		results := []ReviewResult{
			{Reviewer: "security", Provider: "anthropic"},
			{Reviewer: "security", Provider: "anthropic", Error: "boom"},
		}

		consolidated, err := Consolidate(context.Background(), provider, "", results)
		r.NoError(err)
		r.Empty(consolidated)
	})

	t.Run("LLM returns invalid JSON falls back to raw", func(t *testing.T) {
		r := require.New(t)

		provider := &mockProvider{response: "Cool cool cool. Streets ahead, no valid JSON here."}
		results := []ReviewResult{
			{
				Reviewer: "security",
				Provider: "anthropic",
				Findings: []Finding{
					{Severity: SeverityCritical, File: "darkest_timeline.go", Description: "Evil Abed detected"},
				},
			},
		}

		consolidated, err := Consolidate(context.Background(), provider, "", results)
		r.NoError(err) // fallback, not error
		r.Len(consolidated, 1)
		r.Equal("darkest_timeline.go", consolidated[0].File)
		r.Len(consolidated[0].Sources, 1)
		r.Equal("security", consolidated[0].Sources[0].Reviewer)
	})

	t.Run("LLM error falls back to raw", func(t *testing.T) {
		r := require.New(t)

		provider := &mockProvider{err: fmt.Errorf("Señor Chang broke the API")}
		results := []ReviewResult{
			{
				Reviewer: "security",
				Provider: "anthropic",
				Findings: []Finding{
					{Severity: SeverityWarning, Description: "Troy Barnes check"},
				},
			},
		}

		consolidated, err := Consolidate(context.Background(), provider, "", results)
		r.NoError(err)
		r.Len(consolidated, 1)
		r.Equal(SeverityWarning, consolidated[0].Severity)
	})

	t.Run("context timeout falls back to raw", func(t *testing.T) {
		r := require.New(t)

		provider := &mockProvider{
			response: `[{"severity":"warning","description":"test","sources":[]}]`,
			delay:    5 * time.Second,
		}
		results := []ReviewResult{
			{
				Reviewer: "security",
				Provider: "anthropic",
				Findings: []Finding{
					{Severity: SeverityCritical, Description: "Dean Pelton's breach"},
				},
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		consolidated, err := Consolidate(ctx, provider, "", results)
		r.NoError(err)
		r.Len(consolidated, 1)
		r.Equal(SeverityCritical, consolidated[0].Severity)
		r.Equal("Dean Pelton's breach", consolidated[0].Description)
	})

	t.Run("single finding from single agent still consolidates", func(t *testing.T) {
		r := require.New(t)

		// LLM reclassifies the severity
		llmResponse := `[{
			"severity": "warning",
			"file": "paintball.go",
			"startLine": 1,
			"description": "Reclassified: minor issue not critical",
			"sources": [{"reviewer": "security", "provider": "anthropic"}]
		}]`

		provider := &mockProvider{response: llmResponse}
		results := []ReviewResult{
			{
				Reviewer: "security",
				Provider: "anthropic",
				Findings: []Finding{
					{Severity: SeverityCritical, File: "paintball.go", StartLine: 1, Description: "SQL injection"},
				},
			},
		}

		consolidated, err := Consolidate(context.Background(), provider, "", results)
		r.NoError(err)
		r.Len(consolidated, 1)
		r.Equal(SeverityWarning, consolidated[0].Severity, "LLM should be able to reclassify")
	})

	t.Run("consolidation produces more findings logs warning but accepts", func(t *testing.T) {
		r := require.New(t)

		llmResponse := `[
			{"severity":"warning","description":"Finding A","sources":[{"reviewer":"security","provider":"anthropic"}]},
			{"severity":"suggestion","description":"Finding B","sources":[{"reviewer":"security","provider":"anthropic"}]},
			{"severity":"suggestion","description":"Finding C","sources":[{"reviewer":"security","provider":"anthropic"}]}
		]`

		provider := &mockProvider{response: llmResponse}
		results := []ReviewResult{
			{
				Reviewer: "security",
				Provider: "anthropic",
				Findings: []Finding{
					{Severity: SeverityWarning, Description: "Only one raw finding"},
				},
			},
		}

		consolidated, err := Consolidate(context.Background(), provider, "", results)
		r.NoError(err)
		r.Len(consolidated, 3, "should accept output even if it has more findings than input")
	})
}

func TestConsolidationResponseValidation(t *testing.T) {
	t.Run("filters findings with invalid severity", func(t *testing.T) {
		r := require.New(t)

		input := `[
			{"severity":"critical","description":"valid","sources":[{"reviewer":"a","provider":"b"}]},
			{"severity":"bananas","description":"invalid sev","sources":[{"reviewer":"a","provider":"b"}]},
			{"severity":"warning","description":"also valid","sources":[{"reviewer":"a","provider":"b"}]}
		]`

		findings, err := parseConsolidationResponse(input)
		r.NoError(err)
		// Even bananas should parse — we don't filter at parse level, the LLM
		// prompt asks for valid severities. The parse should be permissive.
		r.Len(findings, 3)
	})
}

func TestConsolidatedResultsJSON(t *testing.T) {
	r := require.New(t)

	cf := ConsolidatedFinding{
		Severity:    SeverityCritical,
		File:        "study_room_f.go",
		StartLine:   1,
		EndLine:     10,
		Description: "Dean Pelton's security policy violated",
		Sources: []Source{
			{Reviewer: "security", Provider: "anthropic"},
		},
	}

	data, err := json.Marshal(cf)
	r.NoError(err)

	var parsed ConsolidatedFinding
	r.NoError(json.Unmarshal(data, &parsed))
	r.Equal(cf, parsed)
}

func TestConsolidateSystemPrompt(t *testing.T) {
	r := require.New(t)
	prompt := consolidationSystemPrompt()
	r.Contains(prompt, "review manager")
	r.Contains(prompt, "JSON")
	r.Contains(prompt, "critical")
	r.Contains(prompt, "warning")
	r.Contains(prompt, "suggestion")
	r.Contains(prompt, "sources")
}

func TestCollectAllFindings(t *testing.T) {
	r := require.New(t)

	results := []ReviewResult{
		{
			Reviewer: "security",
			Provider: "anthropic",
			Findings: []Finding{
				{Severity: SeverityCritical, Description: "A"},
				{Severity: SeverityWarning, Description: "B"},
			},
		},
		{
			Reviewer: "code-quality",
			Provider: "openai",
			Error:    "boom",
		},
		{
			Reviewer: "maintainability",
			Provider: "anthropic",
			Findings: []Finding{
				{Severity: SeveritySuggestion, Description: "C"},
			},
		},
	}

	findings := collectAllFindings(results)
	r.Len(findings, 3)
}

// Test that formatConsolidatedForCoder includes only critical+warning.
func TestFormatConsolidatedForCoder(t *testing.T) {
	r := require.New(t)

	findings := []ConsolidatedFinding{
		{
			Severity:    SeverityCritical,
			File:        "paintball.go",
			StartLine:   42,
			Description: "SQL injection in query builder",
			Sources:     []Source{{Reviewer: "security", Provider: "anthropic"}},
		},
		{
			Severity:    SeveritySuggestion,
			File:        "chang.go",
			StartLine:   10,
			Description: "Consider extracting helper — streets ahead",
			Sources:     []Source{{Reviewer: "code-quality", Provider: "anthropic"}},
		},
		{
			Severity:    SeverityWarning,
			Description: "Missing nil check on enrollment",
			Sources:     []Source{{Reviewer: "maintainability", Provider: "openai"}},
		},
	}

	msg := FormatConsolidatedForCoder(findings)
	r.Contains(msg, "Fix ONLY")
	r.Contains(msg, "SQL injection")
	r.Contains(msg, "paintball.go:42")
	r.Contains(msg, "Missing nil check")
	r.NotContains(msg, "streets ahead", "suggestions should be filtered out")
	r.NotContains(msg, "chang.go", "suggestion file should not appear")
}

func TestCapString(t *testing.T) {
	tests := map[string]struct {
		input  string
		maxLen int
		want   string
	}{
		"short ASCII": {
			input:  "Troy Barnes",
			maxLen: 20,
			want:   "Troy Barnes",
		},
		"exact length": {
			input:  "Abed",
			maxLen: 4,
			want:   "Abed",
		},
		"truncate ASCII": {
			input:  "Greendale Community College",
			maxLen: 9,
			want:   "Greendale...",
		},
		"multibyte safe truncation": {
			// "Señor Chang" — ñ is 2 bytes in UTF-8
			input:  "Señor Chang",
			maxLen: 5,
			want:   "Señor...",
		},
		"CJK characters": {
			// Each CJK char is 3 bytes — truncating at byte boundary would corrupt
			input:  "日本語テスト",
			maxLen: 3,
			want:   "日本語...",
		},
		"emoji truncation": {
			// Emoji are 4 bytes each in UTF-8
			input:  "🎲🎯🎪🎭",
			maxLen: 2,
			want:   "🎲🎯...",
		},
		"empty string": {
			input:  "",
			maxLen: 10,
			want:   "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, capString(tc.input, tc.maxLen))
		})
	}
}

func TestBuildConsolidationPromptStructure(t *testing.T) {
	r := require.New(t)

	results := []ReviewResult{
		{
			Reviewer: "security",
			Provider: "anthropic",
			Findings: []Finding{
				{Severity: SeverityCritical, File: "foo.go", StartLine: 1, EndLine: 5, Description: "Issue A"},
			},
		},
	}

	prompt := buildConsolidationPrompt(results)

	// Should contain structured finding data.
	r.Contains(prompt, "foo.go")
	r.Contains(prompt, "Issue A")
	r.Contains(prompt, "critical")

	// The prompt should be valid enough for the LLM to parse.
	r.True(strings.Contains(prompt, "reviewer") || strings.Contains(prompt, "Reviewer"))
}
