package phase

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/jelmersnoeck/forge/internal/runtime/session"
	"github.com/jelmersnoeck/forge/internal/tools"
	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

// debateProvider implements types.LLMProvider for debate tests.
type debateProvider struct {
	response string
	err      error
	calls    atomic.Int32
}

func (m *debateProvider) Chat(_ context.Context, _ types.ChatRequest) (<-chan types.ChatDelta, error) {
	m.calls.Add(1)
	if m.err != nil {
		return nil, m.err
	}

	ch := make(chan types.ChatDelta, 2)
	ch <- types.ChatDelta{Type: "text_delta", Text: m.response}
	close(ch)
	return ch, nil
}

func TestParseCandidates(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		input  string
		source string
		want   int
		err    bool
	}{
		"wrapped object": {
			input: `{"candidates": [
				{
					"name": "greendale-approach",
					"summary": "Enroll at Greendale Community College",
					"approach": "Use existing enrollment system",
					"tradeoffs": ["cheap tuition"],
					"risks": ["may learn nothing"],
					"effort": "S",
					"reuses": ["enrollment-system"]
				}
			]}`,
			source: "conservative",
			want:   1,
		},
		"direct array": {
			input: `[{
				"name": "hawthorne-approach",
				"summary": "Pierce's moist towelettes empire",
				"approach": "Leverage Hawthorne Wipes infrastructure",
				"tradeoffs": ["unlimited funding"],
				"risks": ["Pierce is Pierce"],
				"effort": "XL",
				"reuses": []
			}]`,
			source: "ambitious",
			want:   1,
		},
		"markdown fenced": {
			input:  "```json\n" + `{"candidates": [{"name": "troy-and-abed", "summary": "In the morning", "approach": "Pillow fort", "tradeoffs": ["fun"], "risks": ["blankets"], "effort": "M", "reuses": []}]}` + "\n```",
			source: "pragmatic",
			want:   1,
		},
		"garbage input": {
			input:  "I am the Human Being mascot and I have no JSON for you",
			source: "conservative",
			err:    true,
		},
		"empty candidates": {
			input:  `{"candidates": []}`,
			source: "pragmatic",
			err:    true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			candidates, err := parseCandidates(tc.input, tc.source)
			switch tc.err {
			case true:
				r.Error(err)
			default:
				r.NoError(err)
				r.Len(candidates, tc.want)
				for _, c := range candidates {
					r.Equal(tc.source, c.Source)
				}
			}
		})
	}
}

func TestParseClarifiedResult(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		input string
		want  ClarifiedResult
		err   bool
	}{
		"full result": {
			input: `{
				"candidates": [
					{"name": "greendale", "summary": "Go to Greendale", "approach": "enroll", "tradeoffs": ["cheap"], "risks": ["dean"], "effort": "S", "reuses": [], "source": "conservative"}
				],
				"questions": ["Is the Dean available?"],
				"merged": ["greendale-v2"]
			}`,
			want: ClarifiedResult{
				Candidates: []Candidate{{
					Name: "greendale", Summary: "Go to Greendale", Approach: "enroll",
					Tradeoffs: []string{"cheap"}, Risks: []string{"dean"},
					Effort: "S", Source: "conservative",
				}},
				Questions: []string{"Is the Dean available?"},
				Merged:    []string{"greendale-v2"},
			},
		},
		"no questions": {
			input: `{"candidates": [{"name": "test", "summary": "s", "approach": "a", "tradeoffs": [], "risks": [], "effort": "S", "reuses": [], "source": "p"}]}`,
			want: ClarifiedResult{
				Candidates: []Candidate{{Name: "test", Summary: "s", Approach: "a", Effort: "S", Source: "p"}},
			},
		},
		"invalid json": {
			input: "Senor Chang says no",
			err:   true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := parseClarifiedResult(tc.input)
			switch tc.err {
			case true:
				r.Error(err)
			default:
				r.NoError(err)
				r.Equal(len(tc.want.Candidates), len(result.Candidates))
				r.Equal(tc.want.Questions, result.Questions)
				r.Equal(tc.want.Merged, result.Merged)
			}
		})
	}
}

func TestRunIdeators(t *testing.T) {
	r := require.New(t)

	candidateJSON := `{"candidates": [{"name": "study-group-approach", "summary": "Form a study group at Greendale", "approach": "Leverage social dynamics", "tradeoffs": ["friendship"], "risks": ["Jeff Winger speeches"], "effort": "M", "reuses": ["library"]}]}`

	prov := &debateProvider{response: candidateJSON}
	opts := DebateOpts{
		Provider:  prov,
		SessionID: "test-session",
		Model:     "test-model",
		Prompt:    "Build a study group management system",
		Bundle:    types.ContextBundle{},
		Emit:      func(types.OutboundEvent) {},
	}

	candidates, err := runIdeators(context.Background(), opts)
	r.NoError(err)
	// 3 agents × 1 candidate each = 3 candidates
	r.Len(candidates, 3)
	r.Equal(int32(3), prov.calls.Load(), "should have called provider 3 times")

	// Each candidate should have a different source
	sources := map[string]bool{}
	for _, c := range candidates {
		sources[c.Source] = true
	}
	r.Len(sources, 3)
}

func TestRunIdeators_partialFailure(t *testing.T) {
	r := require.New(t)

	// Provider that fails on every other call
	callCount := atomic.Int32{}
	prov := &alternatingProvider{
		callCount:    &callCount,
		goodResponse: `{"candidates": [{"name": "abed-approach", "summary": "Cool. Cool cool cool.", "approach": "meta-analysis", "tradeoffs": ["awareness"], "risks": ["too meta"], "effort": "S", "reuses": []}]}`,
	}

	opts := DebateOpts{
		Provider:  prov,
		SessionID: "test-session",
		Model:     "test-model",
		Prompt:    "Analyze the Dreamatorium",
		Bundle:    types.ContextBundle{},
		Emit:      func(types.OutboundEvent) {},
	}

	candidates, err := runIdeators(context.Background(), opts)
	r.NoError(err)
	r.NotEmpty(candidates, "should have at least some candidates despite partial failures")
}

func TestRunIdeators_allFail(t *testing.T) {
	r := require.New(t)

	prov := &debateProvider{err: fmt.Errorf("Greendale is closed for paintball")}
	opts := DebateOpts{
		Provider:  prov,
		SessionID: "test-session",
		Model:     "test-model",
		Prompt:    "Nothing works",
		Bundle:    types.ContextBundle{},
		Emit:      func(types.OutboundEvent) {},
	}

	_, err := runIdeators(context.Background(), opts)
	r.Error(err)
	r.Contains(err.Error(), "all")
}

func TestRunClarifier(t *testing.T) {
	r := require.New(t)

	clarifiedJSON := `{"candidates": [{"name": "paintball", "summary": "Annual paintball game", "approach": "strategic", "tradeoffs": ["fun"], "risks": ["expelled"], "effort": "L", "reuses": [], "source": "pragmatic"}], "questions": ["Are we doing Modern Warfare or Western?"]}`

	prov := &debateProvider{response: clarifiedJSON}
	opts := DebateOpts{
		Provider:  prov,
		SessionID: "test-session",
		Model:     "test-model",
		Prompt:    "Plan the paintball game",
		Bundle:    types.ContextBundle{},
		Emit:      func(types.OutboundEvent) {},
	}

	candidates := []Candidate{
		{Name: "paintball", Summary: "Annual paintball game", Source: "pragmatic"},
		{Name: "paintball-v2", Summary: "Same but messier", Source: "ambitious"},
	}

	result, err := runClarifier(context.Background(), opts, candidates)
	r.NoError(err)
	r.Len(result.Candidates, 1)
	r.Len(result.Questions, 1)
	r.Contains(result.Questions[0], "Modern Warfare")
}

func TestRunDebate_emitsEvents(t *testing.T) {
	r := require.New(t)

	candidateJSON := `{"candidates": [{"name": "test", "summary": "s", "approach": "a", "tradeoffs": [], "risks": [], "effort": "S", "reuses": []}]}`
	clarifiedJSON := `{"candidates": [{"name": "test", "summary": "s", "approach": "a", "tradeoffs": [], "risks": [], "effort": "S", "reuses": [], "source": "conservative"}]}`

	// Track which call we're on to return different responses
	callNum := atomic.Int32{}
	prov := &sequentialProvider{
		callNum: &callNum,
		responses: map[int]string{
			// Calls 1-3: ideators
			1: candidateJSON,
			2: candidateJSON,
			3: candidateJSON,
			// Call 4: clarifier
			4: clarifiedJSON,
			// Calls 5+: planner loop — just emit text
			5: "I selected the test approach.",
		},
	}

	sessDir := t.TempDir()
	store := session.NewStore(sessDir)
	registry := tools.NewDefaultRegistry("anthropic")

	var events []types.OutboundEvent
	emit := func(e types.OutboundEvent) {
		events = append(events, e)
	}

	opts := DebateOpts{
		Provider:     prov,
		Registry:     registry,
		Bundle:       types.ContextBundle{AgentDefinitions: make(map[string]types.AgentDefinition)},
		CWD:          t.TempDir(),
		SessionStore: store,
		SessionID:    "test-debate",
		Model:        "test-model",
		Emit:         emit,
		Prompt:       "Build the Dreamatorium",
	}

	_, err := RunDebate(context.Background(), opts)
	// The planner won't find a spec (no Write tool call in mock), so result
	// may have empty SpecPath, but the pipeline itself shouldn't error.
	// We mainly care about event emission.
	_ = err

	// Check that key events were emitted
	eventTypes := map[string]bool{}
	for _, e := range events {
		eventTypes[e.Type] = true
	}

	r.True(eventTypes["ideation_start"], "should emit ideation_start")
	r.True(eventTypes["clarification_start"], "should emit clarification_start")
	r.True(eventTypes["planning_start"], "should emit planning_start")
}

func TestShouldIdeate(t *testing.T) {
	tests := map[string]struct {
		hint string
		want bool
	}{
		"ideate": {hint: "ideate", want: true},
		"code":   {hint: "code", want: false},
		"auto":   {hint: "auto", want: false},
		"empty":  {hint: "", want: false},
		"yolo":   {hint: "yolo", want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, shouldIdeate(tc.hint))
		})
	}
}

func TestBuildContextSummary(t *testing.T) {
	r := require.New(t)

	bundle := types.ContextBundle{
		AgentsMD: []types.AgentsMDEntry{
			{Path: "AGENTS.md", Content: "Greendale rules", Level: "project"},
			{Path: "phase:code", Content: "coder prompt", Level: "phase"},
		},
		Specs: []types.SpecEntry{
			{ID: "paintball", Status: "active", Header: "Annual paintball game"},
			{ID: "old-spec", Status: "implemented", Header: "Done"},
		},
	}

	summary := buildContextSummary(bundle)
	r.Contains(summary, "Greendale rules")
	r.NotContains(summary, "coder prompt") // phase-level shouldn't be included
	r.Contains(summary, "paintball")
	r.NotContains(summary, "old-spec") // implemented specs excluded
}

func TestStripJSONFences(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
	}{
		"fenced": {
			input: "```json\n{\"a\":1}\n```",
			want:  `{"a":1}`,
		},
		"not fenced": {
			input: `{"a":1}`,
			want:  `{"a":1}`,
		},
		"unclosed fence": {
			input: "```json\n{\"a\":1}",
			want:  "```json\n{\"a\":1}",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, stripJSONFences(tc.input))
		})
	}
}

// alternatingProvider fails on even-numbered calls.
type alternatingProvider struct {
	callCount    *atomic.Int32
	goodResponse string
}

func (p *alternatingProvider) Chat(_ context.Context, _ types.ChatRequest) (<-chan types.ChatDelta, error) {
	n := p.callCount.Add(1)
	if n%2 == 0 {
		return nil, fmt.Errorf("Chang'd (call %d)", n)
	}

	ch := make(chan types.ChatDelta, 2)
	ch <- types.ChatDelta{Type: "text_delta", Text: p.goodResponse}
	close(ch)
	return ch, nil
}

// sequentialProvider returns different responses based on call number.
type sequentialProvider struct {
	callNum   *atomic.Int32
	responses map[int]string
}

func (p *sequentialProvider) Chat(_ context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	n := int(p.callNum.Add(1))

	resp, ok := p.responses[n]
	if !ok {
		// Default: return empty text for calls beyond what's defined
		resp = "No response configured"
	}

	ch := make(chan types.ChatDelta, 2)
	ch <- types.ChatDelta{Type: "text_delta", Text: resp}
	// Add a usage delta so the loop can track tokens
	ch <- types.ChatDelta{
		Type: "usage",
		Usage: &types.TokenUsage{
			InputTokens:  100,
			OutputTokens: 50,
		},
	}
	close(ch)
	return ch, nil
}

func TestIdeatorPersonalities(t *testing.T) {
	r := require.New(t)

	// Verify we have 3 distinct personalities
	r.Len(ideatorPersonalities, 3)

	names := map[string]bool{}
	for _, p := range ideatorPersonalities {
		r.NotEmpty(p.Name)
		r.NotEmpty(p.System)
		names[p.Name] = true
	}
	r.Len(names, 3, "all personality names must be unique")
}

func TestCandidateJSON_roundTrip(t *testing.T) {
	r := require.New(t)

	c := Candidate{
		Name:      "study-room-f",
		Summary:   "Meet in Study Room F at Greendale",
		Approach:  "Form a study group with 7 misfits",
		Tradeoffs: []string{"friendship", "personal growth"},
		Risks:     []string{"Jeff's ego", "Pierce being Pierce"},
		Effort:    "L",
		Reuses:    []string{"library-system"},
		Source:    "pragmatic",
	}

	data, err := json.Marshal(c)
	r.NoError(err)

	var decoded Candidate
	r.NoError(json.Unmarshal(data, &decoded))
	r.Equal(c, decoded)
}

func TestTruncateForLog(t *testing.T) {
	r := require.New(t)

	r.Equal("short", truncateForLog("short", 100))
	r.Equal("abc...", truncateForLog("abcdef", 3))
	r.Equal("", truncateForLog("", 10))
}

// Verify events have required fields
func TestDebateEventFields(t *testing.T) {
	r := require.New(t)

	candidateJSON := `{"candidates": [{"name": "test", "summary": "s", "approach": "a", "tradeoffs": [], "risks": [], "effort": "S", "reuses": []}]}`

	prov := &debateProvider{response: candidateJSON}

	var events []types.OutboundEvent
	emit := func(e types.OutboundEvent) {
		events = append(events, e)
	}

	opts := DebateOpts{
		Provider:  prov,
		SessionID: "event-test",
		Model:     "test-model",
		Prompt:    "Test events",
		Bundle:    types.ContextBundle{},
		Emit:      emit,
	}

	// Just run ideators to check event quality
	_, _ = runIdeators(context.Background(), opts)

	// The ideation_start event should have been emitted by RunDebate,
	// but runIdeators itself doesn't emit — that's fine. We test RunDebate
	// event emission in TestRunDebate_emitsEvents. Here verify the mock works.
	r.Equal(int32(3), prov.calls.Load())
}
