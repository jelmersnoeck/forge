package phase

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

// mockProvider implements types.LLMProvider for testing.
type mockProvider struct {
	// responses maps model name to the response behavior.
	// nil value = return error, non-nil = send deltas then close.
	responses map[string][]types.ChatDelta
	// calls records which models were called, in order.
	calls []string
}

// testLightweightModels mirrors the Anthropic provider's cheap-model list. The
// mock implements types.LightweightModeler so provider.LightweightModels()
// returns these, letting tests key responses on the same model names.
var testLightweightModels = []string{
	"claude-haiku-4-5",
	"claude-haiku-4-5-20251001",
}

// LightweightModels makes mockProvider satisfy types.LightweightModeler.
func (m *mockProvider) LightweightModels() []string { return testLightweightModels }

func (m *mockProvider) Chat(_ context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	m.calls = append(m.calls, req.Model)

	deltas, ok := m.responses[req.Model]
	if !ok {
		return nil, fmt.Errorf("model %q unavailable", req.Model)
	}

	ch := make(chan types.ChatDelta, len(deltas))
	for _, d := range deltas {
		ch <- d
	}
	close(ch)
	return ch, nil
}

func TestParseIntent(t *testing.T) {
	tests := map[string]struct {
		input   string
		want    Intent
		wantErr bool
	}{
		"question": {
			input: `{"intent": "question"}`,
			want:  IntentQuestion,
		},
		"task": {
			input: `{"intent": "task"}`,
			want:  IntentTask,
		},
		"investigate": {
			input: `{"intent": "investigate"}`,
			want:  IntentInvestigate,
		},
		"triage": {
			input: `{"intent": "triage"}`,
			want:  IntentTriage,
		},
		"review": {
			input: `{"intent": "review"}`,
			want:  IntentReview,
		},
		"question with whitespace": {
			input: `  {"intent": "question"}  `,
			want:  IntentQuestion,
		},
		"garbage defaults to task": {
			input:   "I'm not sure what you mean",
			want:    IntentTask,
			wantErr: true,
		},
		"empty string defaults to task": {
			input:   "",
			want:    IntentTask,
			wantErr: true,
		},
		"unknown intent value defaults to task": {
			input:   `{"intent": "greendale"}`,
			want:    IntentTask,
			wantErr: true,
		},
		"valid JSON but no intent field": {
			input:   `{"category": "question"}`,
			want:    IntentTask,
			wantErr: true,
		},
		"malformed JSON defaults to task": {
			input:   `{"intent": `,
			want:    IntentTask,
			wantErr: true,
		},
		"JSON with extra fields still works": {
			input: `{"intent": "question", "confidence": 0.9}`,
			want:  IntentQuestion,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got, err := parseIntent(tc.input)
			r.Equal(tc.want, got)
			if tc.wantErr {
				r.Error(err)
			} else {
				r.NoError(err)
			}
		})
	}
}

func TestClassifyIntentEmptyPrompt(t *testing.T) {
	r := require.New(t)
	// Empty prompt should skip classification and return task.
	got, err := ClassifyIntent(t.Context(), nil, "")
	r.NoError(err)
	r.Equal(IntentTask, got)
}

func TestClassifyIntentWhitespacePrompt(t *testing.T) {
	r := require.New(t)
	got, err := ClassifyIntent(t.Context(), nil, "   ")
	r.NoError(err)
	r.Equal(IntentTask, got)
}

func TestClassifyIntentSuccess(t *testing.T) {
	tests := map[string]struct {
		response string
		want     Intent
	}{
		"question": {
			response: `{"intent": "question"}`,
			want:     IntentQuestion,
		},
		"task": {
			response: `{"intent": "task"}`,
			want:     IntentTask,
		},
		"investigate": {
			response: `{"intent": "investigate"}`,
			want:     IntentInvestigate,
		},
		"review": {
			response: `{"intent": "review"}`,
			want:     IntentReview,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			prov := &mockProvider{
				responses: map[string][]types.ChatDelta{
					testLightweightModels[0]: {
						{Type: "text_delta", Text: tc.response},
					},
				},
			}

			got, err := ClassifyIntent(t.Context(), prov, "how does the caching work?")
			r.NoError(err)
			r.Equal(tc.want, got)
			r.Len(prov.calls, 1, "should only try first model on success")
		})
	}
}

func TestClassifyIntentModelFallback(t *testing.T) {
	r := require.New(t)
	r.GreaterOrEqual(len(testLightweightModels), 2, "need at least 2 models for fallback test")

	// Only the last model succeeds; all others are absent from mock → return error.
	lastModel := testLightweightModels[len(testLightweightModels)-1]
	prov := &mockProvider{
		responses: map[string][]types.ChatDelta{
			lastModel: {
				{Type: "text_delta", Text: `{"intent": "question"}`},
			},
		},
	}

	got, err := ClassifyIntent(t.Context(), prov, "what files handle MCP?")
	r.NoError(err)
	r.Equal(IntentQuestion, got)
	r.Len(prov.calls, len(testLightweightModels), "should try all models before succeeding")
}

func TestClassifyIntentAllModelsFail(t *testing.T) {
	r := require.New(t)

	// No models available — all fail.
	prov := &mockProvider{
		responses: map[string][]types.ChatDelta{},
	}

	got, err := ClassifyIntent(t.Context(), prov, "add a verbose flag")
	r.Equal(IntentTask, got, "should default to task on failure")
	r.Error(err)
	r.Contains(err.Error(), "all models failed")
}

func TestClassifyIntentStreamError(t *testing.T) {
	r := require.New(t)

	// Model returns an error delta in the stream.
	prov := &mockProvider{
		responses: map[string][]types.ChatDelta{
			testLightweightModels[0]: {
				{Type: "error", Text: "rate limited, Troy Barnes"},
			},
		},
	}

	got, err := ClassifyIntent(t.Context(), prov, "explain session lifecycle")
	r.Equal(IntentTask, got)
	r.Error(err)
	r.Contains(err.Error(), "all models failed")
}

func TestClassifyIntentGarbageResponse(t *testing.T) {
	r := require.New(t)

	// Model returns valid stream but garbage content.
	prov := &mockProvider{
		responses: map[string][]types.ChatDelta{
			testLightweightModels[0]: {
				{Type: "text_delta", Text: "I don't understand, I'm the Human Being mascot"},
			},
		},
	}

	got, err := ClassifyIntent(t.Context(), prov, "how does routing work?")
	r.Equal(IntentTask, got)
	r.Error(err)
}

func TestClassifyIntentPromptTruncation(t *testing.T) {
	r := require.New(t)

	// Build a long prompt that exceeds maxClassifyPromptLen.
	longPrompt := ""
	for i := 0; i < 200; i++ {
		longPrompt += "Greendale "
	}

	var capturedPrompt string
	prov := &mockProvider{
		responses: map[string][]types.ChatDelta{
			testLightweightModels[0]: {
				{Type: "text_delta", Text: `{"intent": "task"}`},
			},
		},
	}

	// Wrap to capture the prompt sent to the model.
	origChat := prov.Chat
	wrappedProv := &promptCapturingProvider{
		inner:          prov,
		capturedPrompt: &capturedPrompt,
	}
	_ = origChat

	got, err := ClassifyIntent(t.Context(), wrappedProv, longPrompt)
	r.NoError(err)
	r.Equal(IntentTask, got)
	// The prompt sent should be truncated.
	r.LessOrEqual(len([]rune(capturedPrompt)), maxClassifyPromptLen+3) // +3 for "..."
}

// promptCapturingProvider wraps a provider and captures the user and system messages.
type promptCapturingProvider struct {
	inner          *mockProvider
	capturedPrompt *string
	capturedSystem *string
}

// LightweightModels forwards the inner mock's list so provider.LightweightModels
// resolves through the wrapper.
func (p *promptCapturingProvider) LightweightModels() []string { return p.inner.LightweightModels() }

func (p *promptCapturingProvider) Chat(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	if p.capturedPrompt != nil && len(req.Messages) > 0 && len(req.Messages[0].Content) > 0 {
		*p.capturedPrompt = req.Messages[0].Content[0].Text
	}
	if p.capturedSystem != nil && len(req.System) > 0 {
		*p.capturedSystem = req.System[0].Text
	}
	return p.inner.Chat(ctx, req)
}

func TestTruncateAtWordBoundary(t *testing.T) {
	tests := map[string]struct {
		input  string
		maxLen int
		want   string
	}{
		"short string unchanged": {
			input:  "how does caching work",
			maxLen: 100,
			want:   "how does caching work",
		},
		"truncates at word boundary": {
			input:  "how does the caching layer work in production",
			maxLen: 25,
			want:   "how does the caching...",
		},
		"single massive word hard-cuts": {
			input:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			maxLen: 10,
			want:   "aaaaaaaaaa...",
		},
		"exact length unchanged": {
			input:  "hello",
			maxLen: 5,
			want:   "hello",
		},
		"trims trailing whitespace before ellipsis": {
			input:  "Troy Barnes is a football star at Greendale Community College",
			maxLen: 30,
			want:   "Troy Barnes is a football...",
		},
		"multi-byte UTF-8 preserved": {
			input:  "こんにちは世界 hello world",
			maxLen: 8,
			want:   "こんにちは世界...",
		},
		"emoji boundary respected": {
			input:  "🎓🎓🎓🎓🎓 Greendale forever",
			maxLen: 6,
			want:   "🎓🎓🎓🎓🎓...",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got := truncateAtWordBoundary(tc.input, tc.maxLen)
			r.Equal(tc.want, got)
		})
	}
}

func TestLightweightModelsUsed(t *testing.T) {
	r := require.New(t)
	r.NotEmpty(testLightweightModels, "testLightweightModels must have at least one model")
	for _, m := range testLightweightModels {
		r.NotEmpty(m, "model name must not be empty")
	}
}

func TestStripCodeFences(t *testing.T) {
	fence := "```"
	tests := map[string]struct {
		input string
		want  string
	}{
		"no fences": {
			input: `{"intent": "question"}`,
			want:  `{"intent": "question"}`,
		},
		"json fence": {
			input: fence + "json\n" + `{"intent": "question"}` + "\n" + fence,
			want:  `{"intent": "question"}`,
		},
		"bare fence": {
			input: fence + "\n" + `{"intent": "task"}` + "\n" + fence,
			want:  `{"intent": "task"}`,
		},
		"fence with surrounding whitespace": {
			input: "  " + fence + "json\n" + `{"intent": "question"}` + "\n" + fence + "  ",
			want:  `{"intent": "question"}`,
		},
		"fence with no newline after opening": {
			input: fence + `{"intent": "task"}` + fence,
			want:  `{"intent": "task"}`,
		},
		"content with triple backticks returned as-is": {
			// If ``` appears inside the JSON content, don't try to strip —
			// we can't safely determine which backticks are structural.
			input: fence + "json\n" + `{"code": "use ` + fence + ` for blocks"}` + "\n" + fence,
			want:  fence + "json\n" + `{"code": "use ` + fence + ` for blocks"}` + "\n" + fence,
		},
		"only opening fence no closing": {
			input: fence + "json\n" + `{"intent": "task"}`,
			want:  `{"intent": "task"}`,
		},
		"oversized input returned as-is": {
			input: fence + "json\n" + strings.Repeat("x", maxStripInputLen+1) + "\n" + fence,
			want:  fence + "json\n" + strings.Repeat("x", maxStripInputLen+1) + "\n" + fence,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, stripCodeFences(tc.input))
		})
	}
}

func TestParseIntentCodeFenced(t *testing.T) {
	fence := "```"
	tests := map[string]struct {
		input string
		want  Intent
	}{
		"json fence question": {
			input: fence + "json\n" + `{"intent": "question"}` + "\n" + fence,
			want:  IntentQuestion,
		},
		"bare fence task": {
			input: fence + "\n" + `{"intent": "task"}` + "\n" + fence,
			want:  IntentTask,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got, err := parseIntent(tc.input)
			r.NoError(err)
			r.Equal(tc.want, got)
		})
	}
}

func TestParseClassification(t *testing.T) {
	specs := []types.SpecEntry{
		{ID: "paintball", Status: "active", Header: "Annual paintball game"},
		{ID: "study-group", Status: "draft", Header: "Study group management"},
	}

	tests := map[string]struct {
		input   string
		specs   []types.SpecEntry
		want    Classification
		wantErr bool
	}{
		"task with small size": {
			input: `{"intent":"task","size":"small","spec_match":""}`,
			want:  Classification{Intent: IntentTask, Size: TaskSizeSmall},
		},
		"task with standard size": {
			input: `{"intent":"task","size":"standard","spec_match":""}`,
			want:  Classification{Intent: IntentTask, Size: TaskSizeStandard},
		},
		"task with large size": {
			input: `{"intent":"task","size":"large","spec_match":""}`,
			want:  Classification{Intent: IntentTask, Size: TaskSizeLarge},
		},
		"task with valid spec_match": {
			input: `{"intent":"task","size":"standard","spec_match":"paintball"}`,
			specs: specs,
			want:  Classification{Intent: IntentTask, Size: TaskSizeStandard, SpecMatch: "paintball"},
		},
		"task with invalid spec_match is cleared": {
			input: `{"intent":"task","size":"standard","spec_match":"nonexistent"}`,
			specs: specs,
			want:  Classification{Intent: IntentTask, Size: TaskSizeStandard},
		},
		"question ignores size and spec_match": {
			input: `{"intent":"question","size":"large","spec_match":"paintball"}`,
			specs: specs,
			want:  Classification{Intent: IntentQuestion},
		},
		"investigate ignores size and spec_match": {
			input: `{"intent":"investigate","size":"small","spec_match":"study-group"}`,
			specs: specs,
			want:  Classification{Intent: IntentInvestigate},
		},
		"triage ignores size and spec_match": {
			input: `{"intent":"triage","size":"large","spec_match":"paintball"}`,
			specs: specs,
			want:  Classification{Intent: IntentTriage},
		},
		"review ignores size and spec_match": {
			input: `{"intent":"review","size":"standard","spec_match":""}`,
			want:  Classification{Intent: IntentReview},
		},
		"unknown size defaults to standard": {
			input: `{"intent":"task","size":"humongous","spec_match":""}`,
			want:  Classification{Intent: IntentTask, Size: TaskSizeStandard},
		},
		"empty size defaults to standard": {
			input: `{"intent":"task","size":"","spec_match":""}`,
			want:  Classification{Intent: IntentTask, Size: TaskSizeStandard},
		},
		"missing size defaults to standard": {
			input: `{"intent":"task"}`,
			want:  Classification{Intent: IntentTask, Size: TaskSizeStandard},
		},
		"unknown intent defaults to task": {
			input: `{"intent":"greendale","size":"small","spec_match":""}`,
			want:  Classification{Intent: IntentTask, Size: TaskSizeSmall},
		},
		"malformed JSON defaults to task/standard": {
			input:   `not json at all`,
			want:    Classification{Intent: IntentTask, Size: TaskSizeStandard},
			wantErr: true,
		},
		"empty string defaults to task/standard": {
			input:   ``,
			want:    Classification{Intent: IntentTask, Size: TaskSizeStandard},
			wantErr: true,
		},
		"missing intent field defaults to task/standard": {
			input:   `{"size":"small"}`,
			want:    Classification{Intent: IntentTask, Size: TaskSizeStandard},
			wantErr: true,
		},
		"extra fields ignored": {
			input: `{"intent":"task","size":"small","spec_match":"","confidence":0.9}`,
			want:  Classification{Intent: IntentTask, Size: TaskSizeSmall},
		},
		"code fenced JSON": {
			input: "```json\n" + `{"intent":"task","size":"large","spec_match":""}` + "\n```",
			want:  Classification{Intent: IntentTask, Size: TaskSizeLarge},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got, err := parseClassification(tc.input, tc.specs)
			r.Equal(tc.want, got)
			if tc.wantErr {
				r.Error(err)
			} else {
				r.NoError(err)
			}
		})
	}
}

func TestClassifySuccess(t *testing.T) {
	tests := map[string]struct {
		response string
		want     Classification
	}{
		"task small": {
			response: `{"intent":"task","size":"small","spec_match":""}`,
			want:     Classification{Intent: IntentTask, Size: TaskSizeSmall},
		},
		"question": {
			response: `{"intent":"question","size":"","spec_match":""}`,
			want:     Classification{Intent: IntentQuestion},
		},
		"review": {
			response: `{"intent":"review","size":"","spec_match":""}`,
			want:     Classification{Intent: IntentReview},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			prov := &mockProvider{
				responses: map[string][]types.ChatDelta{
					testLightweightModels[0]: {
						{Type: "text_delta", Text: tc.response},
					},
				},
			}

			got, err := Classify(t.Context(), prov, "some prompt", nil)
			r.NoError(err)
			r.Equal(tc.want, got)
		})
	}
}

func TestClassifyWithSpecs(t *testing.T) {
	r := require.New(t)

	specs := []types.SpecEntry{
		{ID: "paintball", Status: "active", Header: "Annual paintball game"},
	}

	var capturedSystem string
	prov := &mockProvider{
		responses: map[string][]types.ChatDelta{
			testLightweightModels[0]: {
				{Type: "text_delta", Text: `{"intent":"task","size":"standard","spec_match":"paintball"}`},
			},
		},
	}

	wrappedProv := &promptCapturingProvider{
		inner:          prov,
		capturedSystem: &capturedSystem,
	}

	got, err := Classify(t.Context(), wrappedProv, "update the paintball scoring", specs)
	r.NoError(err)
	r.Equal(IntentTask, got.Intent)
	r.Equal(TaskSizeStandard, got.Size)
	r.Equal("paintball", got.SpecMatch)
	// The system prompt should contain the spec index.
	r.Contains(capturedSystem, "paintball")
	r.Contains(capturedSystem, "Existing Specs:")
}

func TestClassifySpecMatchValidation(t *testing.T) {
	r := require.New(t)

	specs := []types.SpecEntry{
		{ID: "paintball", Status: "active", Header: "Annual paintball game"},
	}

	// Provider returns a spec_match that doesn't exist in the provided specs.
	prov := &mockProvider{
		responses: map[string][]types.ChatDelta{
			testLightweightModels[0]: {
				{Type: "text_delta", Text: `{"intent":"task","size":"standard","spec_match":"nonexistent"}`},
			},
		},
	}

	got, err := Classify(t.Context(), prov, "do something", specs)
	r.NoError(err)
	r.Equal(IntentTask, got.Intent)
	r.Empty(got.SpecMatch, "invalid spec_match should be cleared")
}

func TestClassifyEmptyPrompt(t *testing.T) {
	r := require.New(t)
	got, err := Classify(t.Context(), nil, "", nil)
	r.NoError(err)
	r.Equal(Classification{Intent: IntentTask, Size: TaskSizeStandard}, got)
}

func TestClassifyAllModelsFail(t *testing.T) {
	r := require.New(t)

	prov := &mockProvider{
		responses: map[string][]types.ChatDelta{},
	}

	got, err := Classify(t.Context(), prov, "add a verbose flag", nil)
	r.Equal(Classification{Intent: IntentTask, Size: TaskSizeStandard}, got)
	r.Error(err)
	r.Contains(err.Error(), "all models failed")
}
