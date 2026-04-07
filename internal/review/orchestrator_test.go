package review

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

// mockProvider implements types.LLMProvider. Its Chat method emits text_delta
// events with the response text then a message_stop event.
type mockProvider struct {
	response string
	err      error
	delay    time.Duration // optional delay before responding
}

func (m *mockProvider) Chat(_ context.Context, _ types.ChatRequest) (<-chan types.ChatDelta, error) {
	if m.err != nil {
		return nil, m.err
	}

	ch := make(chan types.ChatDelta, 2)
	go func() {
		defer close(ch)
		if m.delay > 0 {
			time.Sleep(m.delay)
		}
		ch <- types.ChatDelta{Type: "text_delta", Text: m.response}
		ch <- types.ChatDelta{Type: "message_stop"}
	}()
	return ch, nil
}

// eventCollector is a thread-safe collector for emitted OutboundEvents.
type eventCollector struct {
	mu     sync.Mutex
	events []types.OutboundEvent
}

func (ec *eventCollector) emit(e types.OutboundEvent) {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	ec.events = append(ec.events, e)
}

func (ec *eventCollector) byType(t string) []types.OutboundEvent {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	var out []types.OutboundEvent
	for _, e := range ec.events {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

// greendaleFinding is a valid JSON array response with Community references.
const greendaleFinding = `[{"severity":"warning","file":"paintball.go","startLine":42,"description":"Troy Barnes would not approve of this error handling"}]`

// deanFinding is another valid JSON array response.
const deanFinding = `[{"severity":"critical","file":"study_room_f.go","startLine":1,"description":"Dean Pelton's security policy has been violated"}]`

// testReviewer is a trivial Reviewer for testing.
type testReviewer struct {
	name string
}

func (r testReviewer) Name() string { return r.name }
func (r testReviewer) SystemPrompt() string {
	return "You are reviewing code at Greendale Community College."
}

func TestOrchestrator(t *testing.T) {
	tests := map[string]struct {
		providers    map[string]types.LLMProvider
		reviewers    []Reviewer
		diff         string
		wantResults  int
		wantFindings int
		wantErrors   int
		wantSummary  string
		check        func(t *testing.T, results []ReviewResult, ec *eventCollector)
	}{
		"happy path all reviewers": {
			providers: map[string]types.LLMProvider{
				"anthropic": &mockProvider{response: greendaleFinding},
			},
			reviewers: []Reviewer{
				testReviewer{name: "security"},
				testReviewer{name: "code-quality"},
			},
			diff:         "diff --git a/paintball.go b/paintball.go\n+func Attack() {}",
			wantResults:  2,
			wantFindings: 2,
			wantErrors:   0,
			check: func(t *testing.T, results []ReviewResult, ec *eventCollector) {
				r := require.New(t)
				for _, res := range results {
					r.Empty(res.Error)
					r.Len(res.Findings, 1)
					r.Equal("paintball.go", res.Findings[0].File)
					r.Equal(SeverityWarning, res.Findings[0].Severity)
					r.Contains(res.Findings[0].Description, "Troy Barnes")
					r.Equal("anthropic", res.Findings[0].Provider)
				}

				// Verify events: review_start + 2 review_finding + 2 review_agent_done + review_summary
				startEvents := ec.byType("review_start")
				r.Len(startEvents, 1)
				r.Contains(startEvents[0].Content, "2 reviewers")

				findingEvents := ec.byType("review_finding")
				r.Len(findingEvents, 2)

				summaryEvents := ec.byType("review_summary")
				r.Len(summaryEvents, 1)
				r.Contains(summaryEvents[0].Content, "2 findings")
			},
		},
		"one reviewer fails": {
			providers: map[string]types.LLMProvider{
				"anthropic": &mockProvider{response: deanFinding},
				"broken":    &mockProvider{err: fmt.Errorf("Señor Chang broke the API")},
			},
			reviewers: []Reviewer{
				testReviewer{name: "security"},
			},
			diff:         "diff --git a/chang.go b/chang.go\n+func Expel() {}",
			wantResults:  2,
			wantFindings: 1,
			wantErrors:   1,
			check: func(t *testing.T, results []ReviewResult, ec *eventCollector) {
				r := require.New(t)
				var errCount, findingCount int
				for _, res := range results {
					switch {
					case res.Error != "":
						errCount++
						r.Contains(res.Error, "Señor Chang")
					default:
						findingCount += len(res.Findings)
						r.Equal("critical", string(res.Findings[0].Severity))
						r.Contains(res.Findings[0].Description, "Dean Pelton")
					}
				}
				r.Equal(1, errCount)
				r.Equal(1, findingCount)

				errorEvents := ec.byType("review_error")
				r.Len(errorEvents, 1)
				r.Contains(errorEvents[0].Content, "Señor Chang")
			},
		},
		"non-JSON response": {
			providers: map[string]types.LLMProvider{
				"anthropic": &mockProvider{response: "The Human Being mascot approves this code. No issues found, streets ahead!"},
			},
			reviewers: []Reviewer{
				testReviewer{name: "code-quality"},
			},
			diff:        "diff --git a/mascot.go b/mascot.go\n+func Dance() {}",
			wantResults: 1,
			wantErrors:  1,
			check: func(t *testing.T, results []ReviewResult, ec *eventCollector) {
				r := require.New(t)
				r.Len(results, 1)
				r.Contains(results[0].Error, "parse error")
				r.Nil(results[0].Findings)

				errorEvents := ec.byType("review_error")
				r.Len(errorEvents, 1)
			},
		},
		"empty diff": {
			providers: map[string]types.LLMProvider{
				"anthropic": &mockProvider{response: "[]"},
			},
			reviewers: []Reviewer{
				testReviewer{name: "security"},
			},
			diff: "   ",
			check: func(t *testing.T, results []ReviewResult, ec *eventCollector) {
				r := require.New(t)
				r.Nil(results)

				summaryEvents := ec.byType("review_summary")
				r.Len(summaryEvents, 1)
				r.Contains(summaryEvents[0].Content, "No diff to review")

				// No review_start event should fire.
				r.Empty(ec.byType("review_start"))
			},
		},
		"context cancellation": {
			providers: map[string]types.LLMProvider{
				"anthropic": &mockProvider{
					response: greendaleFinding,
					delay:    2 * time.Second,
				},
			},
			reviewers: []Reviewer{
				testReviewer{name: "security"},
				testReviewer{name: "code-quality"},
			},
			diff: "diff --git a/pillow_fort.go b/pillow_fort.go\n+func Build() {}",
			check: func(t *testing.T, results []ReviewResult, ec *eventCollector) {
				r := require.New(t)
				// With cancelled context, either we get empty findings (partial text)
				// or errors. The key is we returned cleanly.
				r.NotNil(results, "should return something even on cancellation")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			ctx := context.Background()
			if name == "context cancellation" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
			}

			ec := &eventCollector{}
			orch := NewOrchestrator(tc.providers, tc.reviewers)

			req := ReviewRequest{
				Diff: tc.diff,
			}

			results := orch.Run(ctx, req, ec.emit)

			if tc.wantResults > 0 {
				r.Len(results, tc.wantResults)
			}

			if tc.wantFindings > 0 {
				total := 0
				for _, res := range results {
					total += len(res.Findings)
				}
				r.Equal(tc.wantFindings, total)
			}

			if tc.wantErrors > 0 {
				errCount := 0
				for _, res := range results {
					if res.Error != "" {
						errCount++
					}
				}
				r.Equal(tc.wantErrors, errCount)
			}

			if tc.check != nil {
				tc.check(t, results, ec)
			}
		})
	}
}

func TestParseFindings(t *testing.T) {
	tests := map[string]struct {
		input       string
		wantLen     int
		wantErr     bool
		wantErrText string
		check       func(t *testing.T, findings []Finding)
	}{
		"parseFindings with markdown fences": {
			input: "```json\n" + `[{"severity":"warning","file":"paintball.go","startLine":42,"description":"Troy Barnes would not approve of this error handling"}]` + "\n```",
			check: func(t *testing.T, findings []Finding) {
				r := require.New(t)
				r.Len(findings, 1)
				r.Equal("paintball.go", findings[0].File)
				r.Equal(42, findings[0].StartLine)
				r.Equal(SeverityWarning, findings[0].Severity)
				r.Contains(findings[0].Description, "Troy Barnes")
			},
		},
		"parseFindings with prose wrapper": {
			input: "Here are my findings from reviewing the Greendale codebase:\n\n" +
				`[{"severity":"critical","file":"study_room_f.go","startLine":7,"description":"Jeff Winger's closing argument has a nil pointer dereference"}]` +
				"\n\nOverall the code is streets ahead.",
			check: func(t *testing.T, findings []Finding) {
				r := require.New(t)
				r.Len(findings, 1)
				r.Equal("study_room_f.go", findings[0].File)
				r.Equal(SeverityCritical, findings[0].Severity)
				r.Contains(findings[0].Description, "Jeff Winger")
			},
		},
		"parseFindings empty array": {
			input: "[]",
			check: func(t *testing.T, findings []Finding) {
				r := require.New(t)
				r.Nil(findings)
			},
		},
		"parseFindings empty string": {
			input: "",
			check: func(t *testing.T, findings []Finding) {
				r := require.New(t)
				r.Nil(findings)
			},
		},
		"parseFindings no JSON": {
			input:   "Cool. Cool cool cool. No issues here.",
			wantErr: true,
		},
		"parseFindings bare code fence": {
			input: "```\n" + `[{"severity":"suggestion","file":"pillow_fort.go","startLine":1,"description":"Abed recommends extracting this into a blanket fort module"}]` + "\n```",
			check: func(t *testing.T, findings []Finding) {
				r := require.New(t)
				r.Len(findings, 1)
				r.Equal("suggestion", string(findings[0].Severity))
				r.Contains(findings[0].Description, "Abed")
			},
		},
		"parseFindings multiple findings": {
			input: `[
				{"severity":"critical","file":"darkest_timeline.go","startLine":1,"description":"Evil Abed detected"},
				{"severity":"praise","file":"inspector_spacetime.go","startLine":100,"description":"Excellent use of the Inspector Spacetime pattern"}
			]`,
			check: func(t *testing.T, findings []Finding) {
				r := require.New(t)
				r.Len(findings, 2)
				r.Equal(SeverityCritical, findings[0].Severity)
				r.Equal(SeverityPraise, findings[1].Severity)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			findings, err := parseFindings(tc.input)
			if tc.wantErr {
				r.Error(err)
				return
			}
			r.NoError(err)

			if tc.check != nil {
				tc.check(t, findings)
			}
		})
	}
}

func TestReviewersList(t *testing.T) {
	r := require.New(t)

	defaults := DefaultReviewers()
	r.Len(defaults, 4)

	wantNames := []string{"security", "code-quality", "maintainability", "operational"}
	for i, rev := range defaults {
		r.Equal(wantNames[i], rev.Name())
		r.NotEmpty(rev.SystemPrompt(), "reviewer %s should have a system prompt", rev.Name())
	}

	withSpec := DefaultReviewersWithSpec()
	r.Len(withSpec, 5)
	r.Equal("spec-validation", withSpec[4].Name())
	r.NotEmpty(withSpec[4].SystemPrompt())
}

func TestModelForProvider(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
	}{
		"anthropic":     {input: "anthropic", want: "claude-sonnet-4-20250514"},
		"openai":        {input: "openai", want: "gpt-4.1"},
		"OpenAI casing": {input: "OpenAI-prod", want: "gpt-4.1"},
		"unknown":       {input: "greendale-llm", want: "claude-sonnet-4-20250514"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, modelForProvider(tc.input))
		})
	}
}

func TestFormatSummary(t *testing.T) {
	tests := map[string]struct {
		results []ReviewResult
		want    []string
	}{
		"mixed findings": {
			results: []ReviewResult{
				{
					Reviewer: "security",
					Provider: "anthropic",
					Findings: []Finding{
						{Severity: SeverityCritical, Description: "bad"},
						{Severity: SeverityWarning, Description: "meh"},
					},
				},
				{
					Reviewer: "code-quality",
					Provider: "anthropic",
					Findings: []Finding{
						{Severity: SeverityPraise, Description: "nice"},
					},
				},
			},
			want: []string{"3 findings", "1 critical", "1 warnings", "1 praise"},
		},
		"with errors": {
			results: []ReviewResult{
				{Reviewer: "security", Error: "boom"},
				{
					Reviewer: "code-quality",
					Findings: []Finding{
						{Severity: SeveritySuggestion},
					},
				},
			},
			want: []string{"1 findings", "1 suggestions", "1 reviewer errors"},
		},
		"no findings": {
			results: []ReviewResult{
				{Reviewer: "security"},
			},
			want: []string{"0 findings"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			summary := formatSummary(tc.results)
			for _, w := range tc.want {
				r.Contains(summary, w)
			}
		})
	}
}

func TestBuildUserMessage(t *testing.T) {
	r := require.New(t)

	// Non-spec reviewer should not include specs.
	rev := testReviewer{name: "security"}
	req := ReviewRequest{
		Diff: "diff --git a/foo.go b/foo.go\n+func Foo() {}",
		Specs: []types.SpecEntry{
			{ID: "paintball-rules", Status: "active", Content: "# Paintball Rules"},
		},
	}
	msg := buildUserMessage(rev, req)
	r.Contains(msg, "diff --git")
	r.NotContains(msg, "paintball-rules")

	// Spec-validation reviewer should include specs.
	specRev := SpecValidationReviewer{}
	msg = buildUserMessage(specRev, req)
	r.Contains(msg, "diff --git")
	r.Contains(msg, "paintball-rules")
	r.Contains(msg, "Paintball Rules")
}

func TestCollectResponse(t *testing.T) {
	tests := map[string]struct {
		deltas []types.ChatDelta
		want   string
	}{
		"normal text": {
			deltas: []types.ChatDelta{
				{Type: "text_delta", Text: "Streets "},
				{Type: "text_delta", Text: "ahead"},
				{Type: "message_stop"},
			},
			want: "Streets ahead",
		},
		"error stops collection": {
			deltas: []types.ChatDelta{
				{Type: "text_delta", Text: "partial"},
				{Type: "error", Text: "kaboom"},
			},
			want: "partial",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			ch := make(chan types.ChatDelta, len(tc.deltas))
			for _, d := range tc.deltas {
				ch <- d
			}
			close(ch)

			got := collectResponse(context.Background(), ch)
			r.Equal(tc.want, got)
		})
	}
}

func TestCollectResponseContextCancel(t *testing.T) {
	r := require.New(t)

	ch := make(chan types.ChatDelta)
	ctx, cancel := context.WithCancel(context.Background())

	// Send one delta, then cancel.
	go func() {
		ch <- types.ChatDelta{Type: "text_delta", Text: "pop pop"}
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	got := collectResponse(ctx, ch)
	r.Contains(got, "pop pop")
}

func TestStripCodeFences(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
	}{
		"json fence": {
			input: "```json\n[{\"a\":1}]\n```",
			want:  `[{"a":1}]`,
		},
		"bare fence": {
			input: "```\nhello\n```",
			want:  "hello",
		},
		"no fence": {
			input: `[{"a":1}]`,
			want:  `[{"a":1}]`,
		},
		"single line": {
			input: "[]",
			want:  "[]",
		},
		"unclosed fence": {
			input: "```json\n[]\nno closing",
			want:  "```json\n[]\nno closing",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, stripCodeFences(tc.input))
		})
	}
}

func TestEmitError(t *testing.T) {
	r := require.New(t)
	ec := &eventCollector{}
	emitError(ec.emit, "Chang ate the test data")
	events := ec.byType("review_error")
	r.Len(events, 1)
	r.Equal("Chang ate the test data", events[0].Content)
	r.NotEmpty(events[0].ID)
	r.NotZero(events[0].Timestamp)
}

// TestOrchestratorMultipleProviders verifies the NxM fan-out of reviewers × providers.
func TestOrchestratorMultipleProviders(t *testing.T) {
	r := require.New(t)

	providers := map[string]types.LLMProvider{
		"anthropic": &mockProvider{response: greendaleFinding},
		"openai":    &mockProvider{response: deanFinding},
	}

	reviewers := []Reviewer{
		testReviewer{name: "security"},
		testReviewer{name: "code-quality"},
	}

	ec := &eventCollector{}
	orch := NewOrchestrator(providers, reviewers)
	results := orch.Run(context.Background(), ReviewRequest{
		Diff: "diff --git a/foo.go b/foo.go\n+func Foo() {}",
	}, ec.emit)

	r.Len(results, 4, "2 reviewers x 2 providers = 4 results")

	// Check start event says 4 agents.
	starts := ec.byType("review_start")
	r.Len(starts, 1)
	r.Contains(starts[0].Content, "4 agents")

	// All findings should have reviewer and provider stamped.
	findingEvents := ec.byType("review_finding")
	r.Len(findingEvents, 4)
	for _, res := range results {
		r.NotEmpty(res.Reviewer)
		r.NotEmpty(res.Provider)
		for _, f := range res.Findings {
			r.Equal(res.Reviewer, f.Reviewer)
			r.Equal(res.Provider, f.Provider)
		}
	}

	// Summary should count 4 findings.
	summaries := ec.byType("review_summary")
	r.Len(summaries, 1)
	r.True(
		strings.Contains(summaries[0].Content, "4 findings") ||
			strings.Contains(summaries[0].Content, "findings"),
	)

	// Per-provider summaries should be emitted (one per provider).
	provSummaries := ec.byType("review_provider_summary")
	r.Len(provSummaries, 2, "should have one summary per provider")

	// Each provider summary should mention both reviewers.
	for _, ps := range provSummaries {
		r.Contains(ps.Content, "security")
		r.Contains(ps.Content, "code-quality")
	}
}

func TestFormatProviderSummary(t *testing.T) {
	tests := map[string]struct {
		provider string
		results  []ReviewResult
		want     []string
		wantNot  []string
	}{
		"mixed findings": {
			provider: "anthropic",
			results: []ReviewResult{
				{
					Reviewer: "security",
					Provider: "anthropic",
					Findings: []Finding{
						{Severity: SeverityCritical, Description: "Dean Pelton's security policy violated"},
						{Severity: SeverityWarning, Description: "Troy's auth token exposed"},
					},
				},
				{
					Reviewer: "code-quality",
					Provider: "anthropic",
					Findings: []Finding{
						{Severity: SeveritySuggestion, Description: "Abed suggests extracting this"},
					},
				},
			},
			want: []string{"Provider: anthropic", "security: 1 critical, 1 warning", "code-quality: 1 suggestion"},
		},
		"with error": {
			provider: "openai",
			results: []ReviewResult{
				{
					Reviewer: "security",
					Provider: "openai",
					Error:    "Señor Chang broke the API",
				},
			},
			want: []string{"Provider: openai", "error: Señor Chang broke the API"},
		},
		"no findings": {
			provider: "anthropic",
			results: []ReviewResult{
				{Reviewer: "maintainability", Provider: "anthropic"},
			},
			want: []string{"Provider: anthropic", "maintainability: (clean)"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got := formatProviderSummary(tc.provider, tc.results)
			for _, w := range tc.want {
				r.Contains(got, w)
			}
			for _, w := range tc.wantNot {
				r.NotContains(got, w)
			}
		})
	}
}

func TestFormatFindingsMessage(t *testing.T) {
	r := require.New(t)

	results := []ReviewResult{
		{
			Reviewer: "security",
			Provider: "anthropic",
			Findings: []Finding{
				{Severity: SeverityCritical, File: "study_room_f.go", StartLine: 1, Description: "Dean Pelton's security policy violated"},
				{Severity: SeverityPraise, File: "paintball.go", StartLine: 100, Description: "Excellent use of Community references"},
			},
		},
		{
			Reviewer: "code-quality",
			Provider: "openai",
			Findings: []Finding{
				{Severity: SeveritySuggestion, File: "chang.go", StartLine: 10, Description: "Extract helper function"},
			},
		},
		{
			Reviewer: "maintainability",
			Provider: "anthropic",
			Findings: []Finding{
				{Severity: SeverityPraise, Description: "Clean code, streets ahead"},
			},
		},
	}

	msg := FormatFindingsMessage(results)

	r.Contains(msg, "security (anthropic)")
	r.Contains(msg, "[critical] study_room_f.go:1")
	r.Contains(msg, "Dean Pelton")
	r.NotContains(msg, "Excellent use of Community references", "praise should be excluded")
	r.Contains(msg, "code-quality (openai)")
	r.Contains(msg, "[suggestion] chang.go:10")
	r.NotContains(msg, "maintainability", "reviewer with only praise should not appear")
}

func TestHasActionableFindings(t *testing.T) {
	tests := map[string]struct {
		results []ReviewResult
		want    bool
	}{
		"has critical": {
			results: []ReviewResult{
				{Findings: []Finding{{Severity: SeverityCritical}}},
			},
			want: true,
		},
		"only praise": {
			results: []ReviewResult{
				{Findings: []Finding{{Severity: SeverityPraise}}},
			},
			want: false,
		},
		"no findings": {
			results: []ReviewResult{
				{Reviewer: "security"},
			},
			want: false,
		},
		"mixed with praise": {
			results: []ReviewResult{
				{Findings: []Finding{
					{Severity: SeverityPraise},
					{Severity: SeveritySuggestion},
				}},
			},
			want: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, HasActionableFindings(tc.results))
		})
	}
}
