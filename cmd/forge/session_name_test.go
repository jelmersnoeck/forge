package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSanitizeSlug(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
	}{
		"clean slug": {
			input: "fix-auth-timeout",
			want:  "fix-auth-timeout",
		},
		"uppercase": {
			input: "Fix-Auth-Timeout",
			want:  "fix-auth-timeout",
		},
		"spaces become dashes": {
			input: "fix auth timeout",
			want:  "fix-auth-timeout",
		},
		"special chars removed": {
			input: "fix: auth timeout!",
			want:  "fix-auth-timeout",
		},
		"leading trailing dashes trimmed": {
			input: "--fix-auth--",
			want:  "fix-auth",
		},
		"collapsed dashes": {
			input: "fix---auth---timeout",
			want:  "fix-auth-timeout",
		},
		"very long slug truncated": {
			input: "this-is-a-very-long-slug-that-should-be-truncated-at-the-maximum-allowed-length",
			want:  "this-is-a-very-long-slug-that-should-be",
		},
		"empty string": {
			input: "",
			want:  "",
		},
		"only special chars": {
			input: "!@#$%",
			want:  "",
		},
		"backtick wrapped": {
			input: "`fix-auth-timeout`",
			want:  "fix-auth-timeout",
		},
		"newlines and whitespace": {
			input: "\n  fix-auth-timeout  \n",
			want:  "fix-auth-timeout",
		},
		"unicode chars removed": {
			input: "fix-authöTimeout",
			want:  "fix-auth-timeout",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got := sanitizeSlug(tc.input)
			r.Equal(tc.want, got)
		})
	}
}

func TestFallbackSessionName(t *testing.T) {
	r := require.New(t)

	// Generate multiple names and verify format
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		name := fallbackSessionName()
		r.Regexp(`^[a-z]+-[a-z]+$`, name, "fallback name should be adjective-noun: %s", name)
		seen[name] = true
	}

	// With 20x20=400 combos and 50 tries, we should see some variety
	r.Greater(len(seen), 5, "expected variety in fallback names")
}

// sessionNameMockProvider implements types.LLMProvider for testing session naming.
type sessionNameMockProvider struct {
	response []types.ChatDelta
	err      error
	calls    int
	models   []string // records which models were requested
}

func (m *sessionNameMockProvider) Chat(_ context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	m.calls++
	m.models = append(m.models, req.Model)
	if m.err != nil {
		return nil, m.err
	}
	ch := make(chan types.ChatDelta, len(m.response))
	for _, d := range m.response {
		ch <- d
	}
	close(ch)
	return ch, nil
}

// sessionNameModelAwareMock returns different results per model name.
type sessionNameModelAwareMock struct {
	responses map[string][]types.ChatDelta
	calls     []string
}

func (m *sessionNameModelAwareMock) Chat(_ context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
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

func TestGenerateSessionName_EmptyPrompt(t *testing.T) {
	r := require.New(t)

	// Empty prompt should use fallback (no API call)
	prov := &sessionNameMockProvider{}
	name := generateSessionName(prov, "")
	r.Regexp(`^[a-z]+-[a-z]+$`, name, "empty prompt should produce fallback name: %s", name)
	r.Equal(0, prov.calls, "should not call provider for empty prompt")
}

func TestGenerateSessionName_NilProvider(t *testing.T) {
	r := require.New(t)

	name := generateSessionName(nil, "Fix the authentication timeout in the login flow")
	r.Regexp(`^[a-z]+-[a-z]+$`, name, "nil provider should produce fallback name: %s", name)
}

func TestGenerateSessionName_ProviderSuccess(t *testing.T) {
	r := require.New(t)

	prov := &sessionNameMockProvider{
		response: []types.ChatDelta{
			{Type: "text_delta", Text: "fix-auth-timeout"},
		},
	}

	name := generateSessionName(prov, "Fix the authentication timeout in the login flow")
	r.Equal("fix-auth-timeout", name)
	r.Equal(1, prov.calls)
	r.Equal(types.LightweightModels[0], prov.models[0], "should use first lightweight model")
}

func TestGenerateSessionName_ModelFallback(t *testing.T) {
	r := require.New(t)
	r.GreaterOrEqual(len(types.LightweightModels), 2, "need at least 2 models for fallback test")

	// Only the last model succeeds
	lastModel := types.LightweightModels[len(types.LightweightModels)-1]
	prov := &sessionNameModelAwareMock{
		responses: map[string][]types.ChatDelta{
			lastModel: {
				{Type: "text_delta", Text: "paintball-episode"},
			},
		},
	}

	name := generateSessionName(prov, "Plan the annual Greendale paintball game")
	r.Equal("paintball-episode", name)
	r.Len(prov.calls, len(types.LightweightModels), "should try all models")
}

func TestGenerateSessionName_AllModelsFail(t *testing.T) {
	r := require.New(t)

	prov := &sessionNameModelAwareMock{
		responses: map[string][]types.ChatDelta{}, // nothing succeeds
	}

	name := generateSessionName(prov, "This will fail across all models")
	r.Regexp(`^[a-z]+-[a-z]+$`, name, "should fallback to random: %s", name)
	r.Len(prov.calls, len(types.LightweightModels), "should try all models before giving up")
}

func TestGenerateSessionName_ProviderReturnsGarbage(t *testing.T) {
	r := require.New(t)

	prov := &sessionNameMockProvider{
		response: []types.ChatDelta{
			{Type: "text_delta", Text: "!@#$%"},
		},
	}

	name := generateSessionName(prov, "Do something at Greendale Community College")
	r.Regexp(`^[a-z]+-[a-z]+$`, name, "garbage response should fallback: %s", name)
}

func TestGenerateSessionName_ProviderError(t *testing.T) {
	r := require.New(t)

	prov := &sessionNameMockProvider{
		err: fmt.Errorf("Senor Chang says no"),
	}

	name := generateSessionName(prov, "Add retry logic to the MCP client")
	r.Regexp(`^[a-z]+-[a-z]+$`, name, "provider error should fallback: %s", name)
}

func TestGenerateSessionName_StreamError(t *testing.T) {
	r := require.New(t)

	prov := &sessionNameMockProvider{
		response: []types.ChatDelta{
			{Type: "error", Text: "rate limited, Troy Barnes"},
		},
	}

	name := generateSessionName(prov, "Refactor the session loop")
	r.Regexp(`^[a-z]+-[a-z]+$`, name, "stream error should fallback: %s", name)
}

func TestGenerateSessionName_SanitizesResponse(t *testing.T) {
	r := require.New(t)

	prov := &sessionNameMockProvider{
		response: []types.ChatDelta{
			{Type: "text_delta", Text: "  Fix Auth Timeout!  "},
		},
	}

	name := generateSessionName(prov, "Fix the auth timeout")
	r.Equal("fix-auth-timeout", name)
}

func TestDrainTextDeltas(t *testing.T) {
	tests := map[string]struct {
		deltas  []types.ChatDelta
		want    string
		wantErr bool
	}{
		"single delta": {
			deltas: []types.ChatDelta{
				{Type: "text_delta", Text: "hello"},
			},
			want: "hello",
		},
		"multiple deltas": {
			deltas: []types.ChatDelta{
				{Type: "text_delta", Text: "fix-"},
				{Type: "text_delta", Text: "auth-"},
				{Type: "text_delta", Text: "timeout"},
			},
			want: "fix-auth-timeout",
		},
		"error delta": {
			deltas: []types.ChatDelta{
				{Type: "text_delta", Text: "partial"},
				{Type: "error", Text: "Dean Pelton broke it"},
			},
			wantErr: true,
		},
		"empty channel": {
			deltas: []types.ChatDelta{},
			want:   "",
		},
		"oversized response truncated": {
			deltas: []types.ChatDelta{
				{Type: "text_delta", Text: strings.Repeat("a", 600)},
			},
			want: strings.Repeat("a", maxStreamTextLen),
		},
		"multiple deltas exceeding max": {
			deltas: []types.ChatDelta{
				{Type: "text_delta", Text: strings.Repeat("b", 400)},
				{Type: "text_delta", Text: strings.Repeat("c", 400)},
			},
			want: strings.Repeat("b", 400) + strings.Repeat("c", maxStreamTextLen-400),
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

			got, err := drainTextDeltas(ch)
			if tc.wantErr {
				r.Error(err)
				return
			}
			r.NoError(err)
			r.Equal(tc.want, got)
		})
	}
}
