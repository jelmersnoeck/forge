package tokens

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/runtime/provider"
	"github.com/jelmersnoeck/forge/internal/types"
)

const (
	// summarizeTimeout bounds a single summarization call across all model
	// fallback attempts.
	summarizeTimeout = 30 * time.Second
	// maxSummarizeOutputTokens caps the summary length.
	maxSummarizeOutputTokens = 1024
)

// summarizeSystemPrompt instructs the model to produce a terse, lossless-ish
// summary of the messages being dropped during compaction.
const summarizeSystemPrompt = `You are compacting a long agent conversation that no longer fits in the context window. Summarize the messages below so the agent can continue without losing critical context.

Preserve, in priority order:
1. The user's original requirements and overall goal.
2. Key decisions and the reasoning behind them.
3. Files created or modified, and what changed.
4. Errors encountered and how (or whether) they were resolved.
5. Unresolved TODOs or open questions.

Be terse. Use compact bullet points. Omit pleasantries, restated tool output, and anything the agent can re-derive cheaply. Target 500 tokens or fewer. Respond with ONLY the summary text.`

// Summarize produces a terse plain-text summary of the given messages using a
// lightweight model. It tries each model in the provider's lightweight list in order,
// falling through on error. Returns ("", err) on any failure (all models
// errored, timeout, empty response, or a mid-stream error delta); callers must
// tolerate this and fall back to count-only compaction.
//
// Summarize never triggers compaction: it sends only the supplied (already
// bounded) messages plus a small system prompt and does not route through the
// conversation loop. If the input exceeds the budget threshold it is truncated
// from the oldest end before the call.
func Summarize(ctx context.Context, prov types.LLMProvider, msgs []types.ChatMessage, budget Budget) (string, error) {
	if len(msgs) == 0 {
		return "", fmt.Errorf("summarize: no messages to summarize")
	}

	msgs = truncateOldest(msgs, budget.Threshold())

	userText := renderMessages(msgs)
	if strings.TrimSpace(userText) == "" {
		return "", fmt.Errorf("summarize: rendered messages are empty")
	}

	sumCtx, cancel := context.WithTimeout(ctx, summarizeTimeout)
	defer cancel()

	var lastErr error
	models := provider.LightweightModels(prov)
	for i, model := range models {
		summary, err := summarizeWithModel(sumCtx, prov, model, userText)
		if err == nil {
			if i > 0 {
				slog.Info("summarize: succeeded on fallback model", "model", model, "failed_attempts", i)
			}
			return summary, nil
		}
		lastErr = err
		slog.Warn("summarize: model failed", "model", model, "error", err)
	}

	if lastErr == nil {
		return "", fmt.Errorf("summarize: all models failed (none configured)")
	}
	return "", fmt.Errorf("summarize: all models failed: %w", lastErr)
}

// summarizeWithModel runs a single summarization attempt against one model.
func summarizeWithModel(ctx context.Context, prov types.LLMProvider, model, userText string) (string, error) {
	req := types.ChatRequest{
		Model: model,
		System: []types.SystemBlock{
			{Type: "text", Text: summarizeSystemPrompt},
		},
		Messages: []types.ChatMessage{
			{
				Role: "user",
				Content: []types.ChatContentBlock{
					{Type: "text", Text: userText},
				},
			},
		},
		MaxTokens: maxSummarizeOutputTokens,
		Stream:    true,
	}

	deltaChan, err := prov.Chat(ctx, req)
	if err != nil {
		return "", fmt.Errorf("provider.Chat: %w", err)
	}

	var b strings.Builder
	for delta := range deltaChan {
		switch delta.Type {
		case "text_delta":
			b.WriteString(delta.Text)
		case "error":
			return "", fmt.Errorf("stream error: %s", delta.Text)
		}
	}

	summary := strings.TrimSpace(b.String())
	if summary == "" {
		return "", fmt.Errorf("empty summary response")
	}
	return summary, nil
}

// truncateOldest drops messages from the front (oldest) until the estimated
// token count is at or below maxTokens. Always keeps at least the last message.
func truncateOldest(msgs []types.ChatMessage, maxTokens int) []types.ChatMessage {
	if EstimateHistory(msgs) <= maxTokens {
		return msgs
	}
	start := 0
	for start < len(msgs)-1 && EstimateHistory(msgs[start:]) > maxTokens {
		start++
	}
	return msgs[start:]
}

// renderMessages flattens a message slice into a plain-text transcript for the
// summarizer. Tool calls and results are rendered compactly so their intent
// survives. Empty/whitespace text is skipped.
func renderMessages(msgs []types.ChatMessage) string {
	var b strings.Builder
	for _, msg := range msgs {
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				if strings.TrimSpace(block.Text) == "" {
					continue
				}
				fmt.Fprintf(&b, "%s: %s\n", msg.Role, block.Text)
			case "tool_use":
				input := ""
				if block.Input != nil {
					if raw, err := json.Marshal(block.Input); err == nil {
						input = string(raw)
					}
				}
				fmt.Fprintf(&b, "%s [tool_use %s] %s\n", msg.Role, block.Name, input)
			case "tool_result":
				var rb strings.Builder
				for _, rc := range block.Content {
					if rc.Type == "text" {
						rb.WriteString(rc.Text)
					}
				}
				if strings.TrimSpace(rb.String()) == "" {
					continue
				}
				fmt.Fprintf(&b, "%s [tool_result] %s\n", msg.Role, rb.String())
			}
		}
	}
	return b.String()
}
