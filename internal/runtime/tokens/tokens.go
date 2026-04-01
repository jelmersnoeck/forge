// Package tokens provides rough token estimation and context budget tracking.
//
// Token counts use a ~4 bytes-per-token heuristic (same as claude-code's
// fallback estimator). Good enough for budget decisions — off by maybe 15%
// either way, which is fine when we're deciding "compact now vs. next turn."
package tokens

import (
	"encoding/json"
	"fmt"

	"github.com/jelmersnoeck/forge/internal/types"
)

// bytesPerToken is the rough conversion ratio. JSON-heavy content is denser
// (~2 bytes/token) but we use a conservative average.
const bytesPerToken = 4

// Estimate returns a rough token count for a string.
func Estimate(s string) int {
	n := len(s) / bytesPerToken
	if n == 0 && len(s) > 0 {
		return 1
	}
	return n
}

// EstimateMessage returns a rough token count for a single ChatMessage.
func EstimateMessage(msg types.ChatMessage) int {
	total := 0
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			total += Estimate(block.Text)
		case "tool_use":
			total += Estimate(block.Name)
			if block.Input != nil {
				b, _ := json.Marshal(block.Input)
				total += Estimate(string(b))
			}
		case "tool_result":
			for _, rc := range block.Content {
				switch rc.Type {
				case "text":
					total += Estimate(rc.Text)
				case "image":
					// Images are expensive but hard to estimate from base64.
					// Use a fixed cost — the API counts them separately anyway.
					total += 1000
				}
			}
		}
	}
	// Role overhead (~4 tokens for role + formatting).
	total += 4
	return total
}

// EstimateHistory returns rough token count for an entire message history.
func EstimateHistory(messages []types.ChatMessage) int {
	total := 0
	for _, msg := range messages {
		total += EstimateMessage(msg)
	}
	return total
}

// EstimateSystem returns rough token count for the system prompt blocks.
func EstimateSystem(blocks []types.SystemBlock) int {
	total := 0
	for _, b := range blocks {
		total += Estimate(b.Text)
	}
	return total
}

// EstimateTools returns rough token count for tool schemas.
func EstimateTools(schemas []types.ToolSchema) int {
	total := 0
	for _, s := range schemas {
		total += Estimate(s.Name)
		total += Estimate(s.Description)
		if s.InputSchema != nil {
			b, _ := json.Marshal(s.InputSchema)
			total += Estimate(string(b))
		}
	}
	return total
}

// Budget tracks context window capacity and decides when to compact.
//
//	┌──────────────────────────────────────────────────────────┐
//	│                   Context Window (200K)                   │
//	│  ┌─────────┐  ┌──────────────────────┐  ┌────────────┐  │
//	│  │ System   │  │      History         │  │  Reserve   │  │
//	│  │ + Tools  │  │  (grows each turn)   │  │ (output)   │  │
//	│  └─────────┘  └──────────────────────┘  └────────────┘  │
//	│                                                          │
//	│  compact triggers when history pushes into reserve zone   │
//	└──────────────────────────────────────────────────────────┘
type Budget struct {
	ContextWindow int // model's max tokens (e.g. 200000)
	OutputReserve int // reserved for LLM response (e.g. 8192)
	Buffer        int // safety margin before compaction (e.g. 15000)
}

// DefaultBudget returns a budget tuned for Sonnet/Opus with 200K context.
func DefaultBudget() Budget {
	return Budget{
		ContextWindow: 200_000,
		OutputReserve: 8192,
		Buffer:        15_000,
	}
}

// Threshold returns the token count at which compaction should trigger.
func (b Budget) Threshold() int {
	return b.ContextWindow - b.OutputReserve - b.Buffer
}

// ShouldCompact checks whether the current token usage exceeds the budget.
func (b Budget) ShouldCompact(systemTokens, historyTokens, toolTokens int) bool {
	return systemTokens+historyTokens+toolTokens >= b.Threshold()
}

// Compact removes messages from the middle of history to stay within budget.
// Keeps the first message (establishes user intent) and enough recent messages
// to fill ~60% of the available history budget. Returns the compacted history
// and the number of messages removed.
//
// The compacted history includes a boundary marker so the LLM knows context
// was dropped.
func Compact(history []types.ChatMessage, budget Budget, systemTokens, toolTokens int) ([]types.ChatMessage, int) {
	if len(history) <= 2 {
		return history, 0
	}

	targetHistoryTokens := (budget.Threshold() - systemTokens - toolTokens) * 60 / 100

	// Walk backwards from the end, accumulating recent messages until we hit
	// ~60% of the budget. Keep at least the last 2 messages (current turn).
	recentTokens := 0
	keepFromIdx := len(history)
	for i := len(history) - 1; i >= 1; i-- {
		msgTokens := EstimateMessage(history[i])
		if recentTokens+msgTokens > targetHistoryTokens && i < len(history)-2 {
			break
		}
		recentTokens += msgTokens
		keepFromIdx = i
	}

	// If we'd keep everything, no compaction needed.
	if keepFromIdx <= 1 {
		return history, 0
	}

	removed := keepFromIdx - 1 // messages between first and keepFromIdx
	boundary := types.ChatMessage{
		Role: "user",
		Content: []types.ChatContentBlock{{
			Type: "text",
			Text: fmt.Sprintf("[Context note: %d earlier messages were removed to stay within context limits. The conversation continues below.]", removed),
		}},
	}

	compacted := make([]types.ChatMessage, 0, 2+len(history)-keepFromIdx)
	compacted = append(compacted, history[0]) // first message
	compacted = append(compacted, boundary)
	compacted = append(compacted, history[keepFromIdx:]...)

	return compacted, removed
}
