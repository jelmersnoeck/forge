package review

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsSameFinding(t *testing.T) {
	tests := map[string]struct {
		a    Finding
		b    Finding
		want bool
	}{
		"identical findings": {
			a:    Finding{File: "paintball.go", StartLine: 42, Description: "SQL injection in query builder"},
			b:    Finding{File: "paintball.go", StartLine: 42, Description: "SQL injection in query builder"},
			want: true,
		},
		"same file overlapping lines similar description": {
			a:    Finding{File: "paintball.go", StartLine: 40, EndLine: 45, Description: "SQL injection vulnerability in the query builder function"},
			b:    Finding{File: "paintball.go", StartLine: 42, EndLine: 50, Description: "SQL injection in query builder — unsanitized input"},
			want: true,
		},
		"same file adjacent lines similar description": {
			a:    Finding{File: "chang.go", StartLine: 10, Description: "Missing error check on database call"},
			b:    Finding{File: "chang.go", StartLine: 14, Description: "Missing error check on database query"},
			want: true,
		},
		"same file distant lines": {
			a:    Finding{File: "paintball.go", StartLine: 10, Description: "SQL injection in query builder"},
			b:    Finding{File: "paintball.go", StartLine: 200, Description: "SQL injection in query builder"},
			want: false,
		},
		"different files": {
			a:    Finding{File: "paintball.go", StartLine: 42, Description: "SQL injection"},
			b:    Finding{File: "chang.go", StartLine: 42, Description: "SQL injection"},
			want: false,
		},
		"different descriptions same location": {
			a:    Finding{File: "paintball.go", StartLine: 42, Description: "SQL injection in query builder"},
			b:    Finding{File: "paintball.go", StartLine: 42, Description: "Missing nil check before dereferencing pointer"},
			want: false,
		},
		"no file both no lines similar description": {
			a:    Finding{Description: "The codebase lacks comprehensive error handling"},
			b:    Finding{Description: "Codebase lacks comprehensive error handling throughout"},
			want: true,
		},
		"no lines but same file similar description": {
			a:    Finding{File: "study_room_f.go", Description: "Dean Pelton's security policy violated — SQL injection"},
			b:    Finding{File: "study_room_f.go", Description: "Security policy violated with SQL injection vulnerability"},
			want: true,
		},
		"one has lines other doesnt same file similar desc": {
			a:    Finding{File: "troy.go", StartLine: 10, Description: "Missing nil check on Troy Barnes enrollment handler"},
			b:    Finding{File: "troy.go", Description: "Nil check missing on Troy Barnes enrollment handler"},
			want: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, isSameFinding(tc.a, tc.b))
		})
	}
}

func TestLinesOverlap(t *testing.T) {
	tests := map[string]struct {
		startA, endA, startB, endB int
		want                       bool
	}{
		"exact same line": {
			startA: 42, endA: 0, startB: 42, endB: 0,
			want: true,
		},
		"overlapping ranges": {
			startA: 10, endA: 20, startB: 15, endB: 25,
			want: true,
		},
		"adjacent within proximity": {
			startA: 10, endA: 15, startB: 18, endB: 20,
			want: true,
		},
		"beyond proximity": {
			startA: 10, endA: 15, startB: 25, endB: 30,
			want: false,
		},
		"single lines within proximity": {
			startA: 10, endA: 0, startB: 14, endB: 0,
			want: true,
		},
		"single lines beyond proximity": {
			startA: 10, endA: 0, startB: 20, endB: 0,
			want: false,
		},
		"contained range": {
			startA: 10, endA: 50, startB: 20, endB: 30,
			want: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, linesOverlap(tc.startA, tc.endA, tc.startB, tc.endB))
		})
	}
}

func TestDescriptionSimilar(t *testing.T) {
	tests := map[string]struct {
		a, b string
		want bool
	}{
		"identical": {
			a:    "SQL injection in query builder",
			b:    "SQL injection in query builder",
			want: true,
		},
		"similar with different wording": {
			a:    "SQL injection vulnerability in the query builder function",
			b:    "SQL injection in query builder — unsanitized input",
			want: true,
		},
		"completely different": {
			a:    "SQL injection in query builder",
			b:    "Missing nil check on pointer dereference",
			want: false,
		},
		"both empty": {
			a: "", b: "",
			want: true,
		},
		"one empty": {
			a: "SQL injection", b: "",
			want: false,
		},
		"paraphrased same concept": {
			a:    "Missing error handling for database connection failure",
			b:    "Error handling absent for database connection errors",
			want: true,
		},
		"short descriptions different content": {
			a:    "Finding A",
			b:    "Finding B",
			want: false,
		},
		"short descriptions same content": {
			a:    "Finding A",
			b:    "Finding A",
			want: true,
		},
		"two word descriptions different": {
			a:    "SQL injection",
			b:    "nil pointer",
			want: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, descriptionSimilar(tc.a, tc.b))
		})
	}
}

func TestDedupFindings(t *testing.T) {
	t.Run("merges duplicate findings from different reviewers", func(t *testing.T) {
		r := require.New(t)

		results := []ReviewResult{
			{
				Reviewer: "security",
				Provider: "anthropic",
				Findings: []Finding{
					{Severity: SeverityCritical, File: "paintball.go", StartLine: 42, Description: "SQL injection in query builder"},
				},
			},
			{
				Reviewer: "code-quality",
				Provider: "anthropic",
				Findings: []Finding{
					{Severity: SeverityWarning, File: "paintball.go", StartLine: 40, EndLine: 45, Description: "SQL injection vulnerability in the query builder function"},
				},
			},
		}

		consolidated := dedupFindings(results)
		r.Len(consolidated, 1, "should merge duplicate findings into one")
		r.Equal(SeverityCritical, consolidated[0].Severity, "should take highest severity")
		r.Len(consolidated[0].Sources, 2, "should track both sources")
		r.Equal("security", consolidated[0].Sources[0].Reviewer)
		r.Equal("code-quality", consolidated[0].Sources[1].Reviewer)
	})

	t.Run("merges duplicates across providers", func(t *testing.T) {
		r := require.New(t)

		results := []ReviewResult{
			{
				Reviewer: "security",
				Provider: "anthropic",
				Findings: []Finding{
					{Severity: SeverityCritical, File: "paintball.go", StartLine: 42, Description: "SQL injection in query builder"},
				},
			},
			{
				Reviewer: "security",
				Provider: "openai",
				Findings: []Finding{
					{Severity: SeverityCritical, File: "paintball.go", StartLine: 42, Description: "SQL injection in the query builder"},
				},
			},
		}

		consolidated := dedupFindings(results)
		r.Len(consolidated, 1)
		r.Len(consolidated[0].Sources, 2)
		r.Equal("anthropic", consolidated[0].Sources[0].Provider)
		r.Equal("openai", consolidated[0].Sources[1].Provider)
	})

	t.Run("keeps distinct findings separate", func(t *testing.T) {
		r := require.New(t)

		results := []ReviewResult{
			{
				Reviewer: "security",
				Provider: "anthropic",
				Findings: []Finding{
					{Severity: SeverityCritical, File: "paintball.go", StartLine: 42, Description: "SQL injection in query builder"},
					{Severity: SeverityWarning, File: "chang.go", StartLine: 10, Description: "Missing nil check on enrollment"},
				},
			},
			{
				Reviewer: "code-quality",
				Provider: "anthropic",
				Findings: []Finding{
					{Severity: SeveritySuggestion, File: "troy.go", StartLine: 1, Description: "Extract helper for Troy Barnes handler"},
				},
			},
		}

		consolidated := dedupFindings(results)
		r.Len(consolidated, 3, "all distinct findings should survive")
	})

	t.Run("empty results", func(t *testing.T) {
		r := require.New(t)
		consolidated := dedupFindings(nil)
		r.Nil(consolidated)
	})

	t.Run("single finding", func(t *testing.T) {
		r := require.New(t)

		results := []ReviewResult{
			{
				Reviewer: "security",
				Provider: "anthropic",
				Findings: []Finding{
					{Severity: SeverityCritical, File: "darkest_timeline.go", Description: "Evil Abed detected"},
				},
			},
		}

		consolidated := dedupFindings(results)
		r.Len(consolidated, 1)
		r.Len(consolidated[0].Sources, 1)
	})

	t.Run("widens line range on merge", func(t *testing.T) {
		r := require.New(t)

		results := []ReviewResult{
			{
				Reviewer: "security",
				Provider: "anthropic",
				Findings: []Finding{
					{Severity: SeverityWarning, File: "a.go", StartLine: 10, EndLine: 15, Description: "Missing error handling on database call"},
				},
			},
			{
				Reviewer: "code-quality",
				Provider: "anthropic",
				Findings: []Finding{
					{Severity: SeverityWarning, File: "a.go", StartLine: 12, EndLine: 20, Description: "Error handling missing for database call"},
				},
			},
		}

		consolidated := dedupFindings(results)
		r.Len(consolidated, 1)
		r.Equal(10, consolidated[0].StartLine)
		r.Equal(20, consolidated[0].EndLine)
	})

	t.Run("keeps longer description", func(t *testing.T) {
		r := require.New(t)

		results := []ReviewResult{
			{
				Reviewer: "security",
				Provider: "anthropic",
				Findings: []Finding{
					{Severity: SeverityWarning, File: "a.go", StartLine: 10, Description: "SQL injection"},
				},
			},
			{
				Reviewer: "code-quality",
				Provider: "openai",
				Findings: []Finding{
					{Severity: SeverityWarning, File: "a.go", StartLine: 10, Description: "SQL injection vulnerability in the query builder — unsanitized user input passed directly to database query"},
				},
			},
		}

		consolidated := dedupFindings(results)
		r.Len(consolidated, 1)
		r.Contains(consolidated[0].Description, "unsanitized")
	})

	t.Run("three reviewers same finding", func(t *testing.T) {
		r := require.New(t)

		results := []ReviewResult{
			{Reviewer: "security", Provider: "anthropic", Findings: []Finding{
				{Severity: SeverityCritical, File: "x.go", StartLine: 1, Description: "SQL injection in query builder"},
			}},
			{Reviewer: "code-quality", Provider: "anthropic", Findings: []Finding{
				{Severity: SeverityWarning, File: "x.go", StartLine: 1, Description: "SQL injection in the query builder"},
			}},
			{Reviewer: "maintainability", Provider: "openai", Findings: []Finding{
				{Severity: SeverityWarning, File: "x.go", StartLine: 2, Description: "Query builder has SQL injection vulnerability"},
			}},
		}

		consolidated := dedupFindings(results)
		r.Len(consolidated, 1, "three reviewers finding the same issue should merge")
		r.Equal(SeverityCritical, consolidated[0].Severity)
		r.Len(consolidated[0].Sources, 3)
	})
}

func TestDedupConsolidated(t *testing.T) {
	t.Run("merges duplicate consolidated findings", func(t *testing.T) {
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
				Severity:    SeverityWarning,
				File:        "paintball.go",
				StartLine:   42,
				Description: "SQL injection vulnerability in query builder function",
				Sources:     []Source{{Reviewer: "code-quality", Provider: "openai"}},
			},
		}

		deduped := dedupConsolidated(findings)
		r.Len(deduped, 1)
		r.Len(deduped[0].Sources, 2)
		r.Equal(SeverityCritical, deduped[0].Severity)
	})

	t.Run("empty input", func(t *testing.T) {
		r := require.New(t)
		r.Nil(dedupConsolidated(nil))
	})

	t.Run("single finding unchanged", func(t *testing.T) {
		r := require.New(t)

		findings := []ConsolidatedFinding{
			{Severity: SeverityWarning, File: "a.go", Description: "test"},
		}

		deduped := dedupConsolidated(findings)
		r.Len(deduped, 1)
	})

	t.Run("does not mutate input sources slice", func(t *testing.T) {
		r := require.New(t)

		original := []Source{{Reviewer: "security", Provider: "anthropic"}}
		findings := []ConsolidatedFinding{
			{
				Severity:    SeverityWarning,
				File:        "a.go",
				StartLine:   10,
				Description: "SQL injection in query builder",
				Sources:     original,
			},
			{
				Severity:    SeverityWarning,
				File:        "a.go",
				StartLine:   10,
				Description: "SQL injection in the query builder",
				Sources:     []Source{{Reviewer: "code-quality", Provider: "openai"}},
			},
		}

		deduped := dedupConsolidated(findings)
		r.Len(deduped, 1)
		r.Len(deduped[0].Sources, 2)
		// Original should not be mutated.
		r.Len(original, 1)
	})
}

func TestSignificantTokens(t *testing.T) {
	tests := map[string]struct {
		input string
		want  []string
	}{
		"normal sentence": {
			input: "SQL injection in query builder",
			want:  []string{"sql", "injection", "query", "builder"},
		},
		"with punctuation": {
			input: "Dean Pelton's security policy — violated!",
			want:  []string{"dean", "pelton", "security", "policy", "violated"},
		},
		"empty": {
			input: "",
			want:  nil,
		},
		"only stop words": {
			input: "the and for are",
			want:  nil,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got := significantTokens(tc.input)
			r.Equal(tc.want, got)
		})
	}
}

func TestMergeLineRange(t *testing.T) {
	tests := map[string]struct {
		startA, endA, startB, endB int
		wantStart, wantEnd         int
	}{
		"both have ranges": {
			startA: 10, endA: 20, startB: 15, endB: 30,
			wantStart: 10, wantEnd: 30,
		},
		"a has no start": {
			startA: 0, endA: 0, startB: 5, endB: 10,
			wantStart: 5, wantEnd: 10,
		},
		"b has no start": {
			startA: 5, endA: 10, startB: 0, endB: 0,
			wantStart: 5, wantEnd: 10,
		},
		"both zero": {
			startA: 0, endA: 0, startB: 0, endB: 0,
			wantStart: 0, wantEnd: 0,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			start, end := mergeLineRange(tc.startA, tc.endA, tc.startB, tc.endB)
			r.Equal(tc.wantStart, start)
			r.Equal(tc.wantEnd, end)
		})
	}
}

func TestDescriptionSimilarAt(t *testing.T) {
	tests := map[string]struct {
		a, b      string
		threshold float64
		want      bool
	}{
		"passes at low threshold": {
			// "unsanitized", "user", "input", "could", "lead", "xss", "attacks"
			// vs "input", "validation", "missing", "potential", "cross", "site", "scripting", "vulnerability"
			// overlap: "input" = 1/6 = 16.7% — passes at 15% but not 30%
			a:         "Unsanitized user input could lead to XSS attacks",
			b:         "Input validation missing, potential cross-site scripting vulnerability",
			threshold: 0.15,
			want:      true,
		},
		"fails at high threshold": {
			a:         "Unsanitized user input could lead to XSS attacks",
			b:         "Input validation missing, potential cross-site scripting vulnerability",
			threshold: 0.30,
			want:      false,
		},
		"rephrased error handling at 40 pct": {
			// "missing", "error", "handling", "database", "connection", "failure"
			// vs "error", "handling", "absent", "database", "connection", "errors"
			// overlap: "error", "handling", "database", "connection" = 4/6 = 66.7%
			a:         "Missing error handling for database connection failure",
			b:         "Error handling absent for database connection errors",
			threshold: 0.40,
			want:      true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, descriptionSimilarAt(tc.a, tc.b, tc.threshold))
		})
	}
}

func TestIsSameFindingTieredThresholds(t *testing.T) {
	tests := map[string]struct {
		a    Finding
		b    Finding
		want bool
	}{
		"colocated lines lenient match": {
			// Same file, overlapping lines: uses 30% threshold.
			// "missing", "input", "validation", "user", "form", "handler"
			// vs "form", "handler", "lacks", "proper", "sanitization", "user", "data"
			// overlap: "user", "form", "handler" = 3/6 = 50% — passes 30%
			a:    Finding{File: "greendale.go", StartLine: 10, EndLine: 15, Description: "Missing input validation in user form handler"},
			b:    Finding{File: "greendale.go", StartLine: 12, EndLine: 18, Description: "Form handler lacks proper sanitization of user data"},
			want: true,
		},
		"no lines moderate threshold rejects low overlap": {
			// Same file, no lines: uses 40% threshold.
			// "unsanitized", "user", "input", "lead", "xss", "attacks" (6 tokens)
			// vs "input", "validation", "missing", "potential", "cross", "site", "scripting", "vulnerability" (8 tokens)
			// overlap: "input" = 1/6 = 16.7% — fails 40%
			a:    Finding{File: "paintball.go", Description: "Unsanitized user input could lead to XSS attacks"},
			b:    Finding{File: "paintball.go", Description: "Input validation missing, potential cross-site scripting vulnerability"},
			want: false,
		},
		"colocated lines rejects completely different": {
			// Same location but completely different issues should not merge.
			a:    Finding{File: "abed.go", StartLine: 10, Description: "SQL injection in query builder"},
			b:    Finding{File: "abed.go", StartLine: 12, Description: "Missing nil check on pointer dereference"},
			want: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, isSameFinding(tc.a, tc.b))
		})
	}
}

func TestDedupRawFindings(t *testing.T) {
	r := require.New(t)

	results := []ReviewResult{
		{
			Reviewer: "security",
			Provider: "anthropic",
			Findings: []Finding{
				{Severity: SeverityCritical, File: "study_room_f.go", StartLine: 42, Description: "SQL injection in query builder"},
			},
		},
		{
			Reviewer: "code-quality",
			Provider: "anthropic",
			Findings: []Finding{
				{Severity: SeverityWarning, File: "study_room_f.go", StartLine: 40, EndLine: 45, Description: "SQL injection vulnerability in the query builder function"},
			},
		},
		{
			Reviewer: "maintainability",
			Provider: "anthropic",
			Findings: []Finding{
				{Severity: SeveritySuggestion, File: "chang.go", StartLine: 100, Description: "Senor Chang approves this function but it could use comments"},
			},
		},
	}

	consolidated := DedupRawFindings(results)
	r.Len(consolidated, 2, "should merge the two SQL injection findings, keep chang.go separate")
	// The SQL injection finding should have 2 sources.
	sqlFinding := consolidated[0]
	r.Equal(SeverityCritical, sqlFinding.Severity)
	r.Len(sqlFinding.Sources, 2)
}
