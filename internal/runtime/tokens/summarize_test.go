package tokens

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

// fakeProvider implements types.LLMProvider for summarization tests.
// responses maps model name to deltas; a missing model returns an error.
type fakeProvider struct {
	responses map[string][]types.ChatDelta
	calls     []string
}

// testLightweightModels mirrors the Anthropic cheap-model list; fakeProvider
// reports it via types.LightweightModeler so provider.LightweightModels()
// returns the names the tests key responses on.
var testLightweightModels = []string{
	"claude-haiku-4-5",
	"claude-haiku-4-5-20251001",
}

func (f *fakeProvider) LightweightModels() []string { return testLightweightModels }

func (f *fakeProvider) Chat(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	f.calls = append(f.calls, req.Model)
	deltas, ok := f.responses[req.Model]
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

func textDelta(s string) types.ChatDelta { return types.ChatDelta{Type: "text_delta", Text: s} }

func userMsg(text string) types.ChatMessage {
	return types.ChatMessage{Role: "user", Content: []types.ChatContentBlock{{Type: "text", Text: text}}}
}

func TestSummarize(t *testing.T) {
	primary := testLightweightModels[0]
	fallback := testLightweightModels[1]

	tests := map[string]struct {
		provider *fakeProvider
		msgs     []types.ChatMessage
		want     string
		wantErr  bool
	}{
		"primary model succeeds": {
			provider: &fakeProvider{responses: map[string][]types.ChatDelta{
				primary: {textDelta("Troy wants the Human Being mascot redesigned.")},
			}},
			msgs: []types.ChatMessage{userMsg("Redesign the Human Being mascot for Greendale.")},
			want: "Troy wants the Human Being mascot redesigned.",
		},
		"falls through to fallback model": {
			provider: &fakeProvider{responses: map[string][]types.ChatDelta{
				fallback: {textDelta("Abed documented the study group's plan.")},
			}},
			msgs: []types.ChatMessage{userMsg("Document the study group plan.")},
			want: "Abed documented the study group's plan.",
		},
		"all models fail": {
			provider: &fakeProvider{responses: map[string][]types.ChatDelta{}},
			msgs:     []types.ChatMessage{userMsg("Señor Chang teaches Spanish.")},
			wantErr:  true,
		},
		"empty response is an error": {
			provider: &fakeProvider{responses: map[string][]types.ChatDelta{
				primary:  {textDelta("   ")},
				fallback: {textDelta("")},
			}},
			msgs:    []types.ChatMessage{userMsg("Greendale paintball war.")},
			wantErr: true,
		},
		"stream error delta": {
			provider: &fakeProvider{responses: map[string][]types.ChatDelta{
				primary:  {{Type: "error", Text: "rate limited"}},
				fallback: {{Type: "error", Text: "rate limited"}},
			}},
			msgs:    []types.ChatMessage{userMsg("Pierce funds a new wing.")},
			wantErr: true,
		},
		"no messages": {
			provider: &fakeProvider{responses: map[string][]types.ChatDelta{}},
			msgs:     nil,
			wantErr:  true,
		},
		"messages with only empty text": {
			provider: &fakeProvider{responses: map[string][]types.ChatDelta{
				primary: {textDelta("ignored")},
			}},
			msgs:    []types.ChatMessage{userMsg("   ")},
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got, err := Summarize(context.Background(), tc.provider, tc.msgs, DefaultBudget())
			if tc.wantErr {
				r.Error(err)
				r.Empty(got)
				return
			}
			r.NoError(err)
			r.Equal(tc.want, got)
		})
	}
}

func TestRenderMessages(t *testing.T) {
	r := require.New(t)
	msgs := []types.ChatMessage{
		userMsg("Build the Greendale study room booking tool."),
		{Role: "assistant", Content: []types.ChatContentBlock{
			{Type: "tool_use", Name: "Write", Input: map[string]any{"path": "booking.go"}},
		}},
		{Role: "user", Content: []types.ChatContentBlock{
			{Type: "tool_result", Content: []types.ToolResultContent{{Type: "text", Text: "wrote booking.go"}}},
		}},
		{Role: "assistant", Content: []types.ChatContentBlock{{Type: "text", Text: "   "}}}, // skipped
	}

	out := renderMessages(msgs)
	r.Contains(out, "user: Build the Greendale study room booking tool.")
	r.Contains(out, "[tool_use Write]")
	r.Contains(out, "booking.go")
	r.Contains(out, "[tool_result] wrote booking.go")
	// Empty assistant text must not produce a dangling "assistant: " line.
	r.NotContains(out, "assistant:    ")
}

func TestTruncateOldest(t *testing.T) {
	r := require.New(t)
	// Three ~heavy messages; cap small enough to force dropping the front.
	big := strings.Repeat("x", 400) // ~100 tokens each
	msgs := []types.ChatMessage{userMsg(big), userMsg(big), userMsg(big)}

	full := EstimateHistory(msgs)
	got := truncateOldest(msgs, full) // fits exactly → unchanged
	r.Len(got, 3)

	got = truncateOldest(msgs, EstimateMessage(msgs[0])+1) // only last fits
	r.Len(got, 1)
	r.Equal(big, got[0].Content[0].Text)

	// Always keeps at least one message even with an impossibly small cap.
	got = truncateOldest(msgs, 1)
	r.Len(got, 1)
}
