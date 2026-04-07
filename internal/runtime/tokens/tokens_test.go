package tokens

import (
	"strings"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestEstimate(t *testing.T) {
	tests := map[string]struct {
		input string
		want  int
	}{
		"empty string":    {input: "", want: 0},
		"short string":    {input: "hi", want: 1},
		"4 bytes = 1 tok": {input: "abcd", want: 1},
		"8 bytes = 2 tok": {input: "abcdefgh", want: 2},
		"greendale speech": {
			input: "Welcome to Greendale Community College, where our Human Being mascot represents all of humanity equally!",
			want:  26, // 104 bytes / 4
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, Estimate(tc.input))
		})
	}
}

func TestEstimateMessage(t *testing.T) {
	tests := map[string]struct {
		msg  types.ChatMessage
		want int
	}{
		"simple text message": {
			msg: types.ChatMessage{
				Role: "user",
				Content: []types.ChatContentBlock{
					{Type: "text", Text: "Troy and Abed in the morning!"},
				},
			},
			want: Estimate("Troy and Abed in the morning!") + 4,
		},
		"tool use message": {
			msg: types.ChatMessage{
				Role: "assistant",
				Content: []types.ChatContentBlock{
					{Type: "tool_use", Name: "Read", Input: map[string]any{"file_path": "/tmp/test.txt"}},
				},
			},
			// Name + JSON input + role overhead
			want: Estimate("Read") + Estimate(`{"file_path":"/tmp/test.txt"}`) + 4,
		},
		"tool result message": {
			msg: types.ChatMessage{
				Role: "user",
				Content: []types.ChatContentBlock{
					{Type: "tool_result", Content: []types.ToolResultContent{
						{Type: "text", Text: "Cool cool cool"},
					}},
				},
			},
			want: Estimate("Cool cool cool") + 4,
		},
		"image in tool result": {
			msg: types.ChatMessage{
				Role: "user",
				Content: []types.ChatContentBlock{
					{Type: "tool_result", Content: []types.ToolResultContent{
						{Type: "image", Source: &types.ImageSource{Data: "base64data"}},
					}},
				},
			},
			want: 1000 + 4, // fixed image cost + role overhead
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, EstimateMessage(tc.msg))
		})
	}
}

func TestEstimateHistory(t *testing.T) {
	r := require.New(t)

	history := []types.ChatMessage{
		{Role: "user", Content: []types.ChatContentBlock{{Type: "text", Text: "Hello"}}},
		{Role: "assistant", Content: []types.ChatContentBlock{{Type: "text", Text: "Hi there"}}},
	}

	total := EstimateHistory(history)
	r.Equal(EstimateMessage(history[0])+EstimateMessage(history[1]), total)
}

func TestBudget(t *testing.T) {
	tests := map[string]struct {
		budget        Budget
		system        int
		history       int
		tools         int
		shouldCompact bool
	}{
		"well under budget": {
			budget:        DefaultBudget(),
			system:        5000,
			history:       50000,
			tools:         5000,
			shouldCompact: false,
		},
		"at threshold": {
			budget:        DefaultBudget(),
			system:        5000,
			history:       DefaultBudget().Threshold() - 5000 - 5000,
			tools:         5000,
			shouldCompact: true,
		},
		"over budget": {
			budget:        DefaultBudget(),
			system:        10000,
			history:       180000,
			tools:         10000,
			shouldCompact: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.shouldCompact, tc.budget.ShouldCompact(tc.system, tc.history, tc.tools))
		})
	}
}

func TestCompact(t *testing.T) {
	// Use a tiny budget so compaction triggers on modest history.
	tinyBudget := Budget{ContextWindow: 10000, OutputReserve: 1000, Buffer: 500}

	tests := map[string]struct {
		historyLen     int
		wantRemoved    bool
		wantBoundary   bool
		wantFirstKept  bool
		wantRecentKept bool
	}{
		"too short to compact": {
			historyLen:  2,
			wantRemoved: false,
		},
		"long history gets compacted": {
			historyLen:     20,
			wantRemoved:    true,
			wantBoundary:   true,
			wantFirstKept:  true,
			wantRecentKept: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			var history []types.ChatMessage
			for i := 0; i < tc.historyLen; i++ {
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				// Each message ~2500 tokens so 20 msgs = ~50K tokens
				history = append(history, types.ChatMessage{
					Role: role,
					Content: []types.ChatContentBlock{
						{Type: "text", Text: strings.Repeat("Greendale Community College ", 350)},
					},
				})
			}

			compacted, removed := Compact(history, tinyBudget, 500, 500)

			switch {
			case !tc.wantRemoved:
				r.Equal(0, removed)
				r.Len(compacted, tc.historyLen)
			default:
				r.Greater(removed, 0)

				if tc.wantFirstKept {
					// First message preserved.
					r.Equal(history[0].Content[0].Text, compacted[0].Content[0].Text)
				}

				if tc.wantBoundary {
					// Second message is the boundary marker.
					r.Contains(compacted[1].Content[0].Text, "earlier messages were removed")
				}

				if tc.wantRecentKept {
					// Last message preserved.
					lastOriginal := history[len(history)-1]
					lastCompacted := compacted[len(compacted)-1]
					r.Equal(lastOriginal.Content[0].Text, lastCompacted.Content[0].Text)
				}
			}
		})
	}
}

func TestCompactPreservesToolPairs(t *testing.T) {
	r := require.New(t)

	// Build a history where compaction boundary would naturally fall between
	// an assistant (tool_use) and user (tool_result).
	//
	// Layout: [user, assistant+tool_use, user+tool_result, ..., assistant, user]
	// The pair at indices 1-2 must stay together or be removed together.
	tinyBudget := Budget{ContextWindow: 10000, OutputReserve: 1000, Buffer: 500}

	bigText := strings.Repeat("Pop pop — Magnitude at Greendale ", 300)

	history := []types.ChatMessage{
		// 0: initial user message
		{Role: "user", Content: []types.ChatContentBlock{{Type: "text", Text: "Help me with the Dreamatorium"}}},
		// 1: assistant with tool_use
		{Role: "assistant", Content: []types.ChatContentBlock{
			{Type: "text", Text: bigText},
			{Type: "tool_use", ID: "toolu_chang", Name: "Read", Input: map[string]any{"file_path": "/senor/chang.txt"}},
		}},
		// 2: user with tool_result
		{Role: "user", Content: []types.ChatContentBlock{
			{Type: "tool_result", ToolUseID: "toolu_chang", Content: []types.ToolResultContent{{Type: "text", Text: bigText}}},
		}},
		// 3-8: more conversation (forces compaction to remove middle)
		{Role: "assistant", Content: []types.ChatContentBlock{{Type: "text", Text: bigText}}},
		{Role: "user", Content: []types.ChatContentBlock{{Type: "text", Text: bigText}}},
		{Role: "assistant", Content: []types.ChatContentBlock{{Type: "text", Text: bigText}}},
		{Role: "user", Content: []types.ChatContentBlock{{Type: "text", Text: bigText}}},
		{Role: "assistant", Content: []types.ChatContentBlock{{Type: "text", Text: bigText}}},
		{Role: "user", Content: []types.ChatContentBlock{{Type: "text", Text: "What does Dean Pelton want?"}}},
	}

	compacted, removed := Compact(history, tinyBudget, 500, 500)
	r.Greater(removed, 0, "should have compacted something")

	// Verify no tool_result message appears without a preceding tool_use message.
	for i, msg := range compacted {
		if !hasToolResults(msg) {
			continue
		}

		r.Greater(i, 0, "tool_result should not be the first message")
		prev := compacted[i-1]
		r.Equal("assistant", prev.Role, "tool_result must follow an assistant message")
		r.True(hasToolUse(prev), "preceding assistant must have tool_use blocks")
	}
}

func TestSanitizeHistory(t *testing.T) {
	tests := map[string]struct {
		history []types.ChatMessage
		want    int // expected message count after sanitization
		check   func(*testing.T, []types.ChatMessage)
	}{
		"empty history": {
			history: nil,
			want:    0,
		},
		"clean history unchanged": {
			history: []types.ChatMessage{
				{Role: "user", Content: []types.ChatContentBlock{{Type: "text", Text: "Cool cool cool"}}},
				{Role: "assistant", Content: []types.ChatContentBlock{{Type: "text", Text: "Indeed"}}},
			},
			want: 2,
		},
		"orphaned tool_result at start": {
			history: []types.ChatMessage{
				{Role: "user", Content: []types.ChatContentBlock{
					{Type: "tool_result", ToolUseID: "toolu_ghost", Content: []types.ToolResultContent{{Type: "text", Text: "no match"}}},
				}},
				{Role: "assistant", Content: []types.ChatContentBlock{{Type: "text", Text: "ok"}}},
			},
			want: 1, // tool_result dropped, empty user message dropped, assistant kept
			check: func(t *testing.T, result []types.ChatMessage) {
				r := require.New(t)
				r.Equal("assistant", result[0].Role)
			},
		},
		"tool_result with mismatched IDs": {
			history: []types.ChatMessage{
				{Role: "user", Content: []types.ChatContentBlock{{Type: "text", Text: "Hello"}}},
				{Role: "assistant", Content: []types.ChatContentBlock{
					{Type: "tool_use", ID: "toolu_abed", Name: "Read"},
				}},
				{Role: "user", Content: []types.ChatContentBlock{
					{Type: "tool_result", ToolUseID: "toolu_abed", Content: []types.ToolResultContent{{Type: "text", Text: "match"}}},
					{Type: "tool_result", ToolUseID: "toolu_troy", Content: []types.ToolResultContent{{Type: "text", Text: "orphan"}}},
				}},
			},
			want: 3,
			check: func(t *testing.T, result []types.ChatMessage) {
				r := require.New(t)
				// Only one tool_result should survive
				r.Len(result[2].Content, 1)
				r.Equal("toolu_abed", result[2].Content[0].ToolUseID)
			},
		},
		"trailing assistant with tool_use dropped": {
			history: []types.ChatMessage{
				{Role: "user", Content: []types.ChatContentBlock{{Type: "text", Text: "Hello"}}},
				{Role: "assistant", Content: []types.ChatContentBlock{
					{Type: "text", Text: "Let me check"},
					{Type: "tool_use", ID: "toolu_britta", Name: "Bash"},
				}},
			},
			want: 1,
			check: func(t *testing.T, result []types.ChatMessage) {
				r := require.New(t)
				r.Equal("user", result[0].Role)
			},
		},
		"trailing assistant without tool_use kept": {
			history: []types.ChatMessage{
				{Role: "user", Content: []types.ChatContentBlock{{Type: "text", Text: "Hello"}}},
				{Role: "assistant", Content: []types.ChatContentBlock{
					{Type: "text", Text: "Streets ahead, Pierce"},
				}},
			},
			want: 2,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			result := SanitizeHistory(tc.history)
			r.Len(result, tc.want)
			if tc.check != nil {
				tc.check(t, result)
			}
		})
	}
}
