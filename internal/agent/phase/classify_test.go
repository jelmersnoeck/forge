package phase

import (
	"context"
	"fmt"
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
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			prov := &mockProvider{
				responses: map[string][]types.ChatDelta{
					types.LightweightModels[0]: {
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
	r.GreaterOrEqual(len(types.LightweightModels), 2, "need at least 2 models for fallback test")

	// Only the last model succeeds; all others are absent from mock → return error.
	lastModel := types.LightweightModels[len(types.LightweightModels)-1]
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
	r.Len(prov.calls, len(types.LightweightModels), "should try all models before succeeding")
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
			types.LightweightModels[0]: {
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
			types.LightweightModels[0]: {
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
			types.LightweightModels[0]: {
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

// promptCapturingProvider wraps a provider and captures the user message.
type promptCapturingProvider struct {
	inner          *mockProvider
	capturedPrompt *string
}

func (p *promptCapturingProvider) Chat(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	if len(req.Messages) > 0 && len(req.Messages[0].Content) > 0 {
		*p.capturedPrompt = req.Messages[0].Content[0].Text
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
	r.NotEmpty(types.LightweightModels, "types.LightweightModels must have at least one model")
	for _, m := range types.LightweightModels {
		r.NotEmpty(m, "model name must not be empty")
	}
}
