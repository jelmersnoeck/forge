package phase

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
)

// Intent represents the classified user intent.
type Intent string

const (
	IntentQuestion Intent = "question"
	IntentTask     Intent = "task"
)

// classificationModel is the lightweight model used for intent classification.
// Same approach as session naming — cheap and fast.
const classificationModel = "claude-haiku-4-20250414"

// classificationSystemPrompt is kept minimal to stay within ~200 input tokens.
const classificationSystemPrompt = `Classify the user's message as either a question (informational, exploratory, "how does X work?") or a task (actionable request to build, fix, change, implement something).

Respond with ONLY a JSON object: {"intent": "question"} or {"intent": "task"}

If ambiguous, prefer "task".`

// maxClassifyPromptLen caps the user prompt sent to the classifier.
// ~1000 chars keeps us well within the ~200 input token budget.
const maxClassifyPromptLen = 1000

// ClassifyIntent uses a lightweight LLM call to determine whether the user's
// prompt is an informational question or an actionable task request.
// Returns IntentTask on any error (safe default).
func ClassifyIntent(ctx context.Context, provider types.LLMProvider, prompt string) Intent {
	if strings.TrimSpace(prompt) == "" {
		return IntentTask
	}

	// Truncate long prompts to stay within token budget.
	classifyPrompt := prompt
	if len(classifyPrompt) > maxClassifyPromptLen {
		classifyPrompt = classifyPrompt[:maxClassifyPromptLen] + "..."
	}

	// Tight timeout — classification must be fast (<500ms target).
	classifyCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req := types.ChatRequest{
		Model: classificationModel,
		System: []types.SystemBlock{
			{Type: "text", Text: classificationSystemPrompt},
		},
		Messages: []types.ChatMessage{
			{
				Role: "user",
				Content: []types.ChatContentBlock{
					{Type: "text", Text: classifyPrompt},
				},
			},
		},
		MaxTokens: 32,
		Stream:    true,
	}

	deltaChan, err := provider.Chat(classifyCtx, req)
	if err != nil {
		log.Printf("[classify] provider error (defaulting to task): %v", err)
		return IntentTask
	}

	// Drain deltas and collect text.
	var text strings.Builder
	for delta := range deltaChan {
		switch delta.Type {
		case "text_delta":
			text.WriteString(delta.Text)
		case "error":
			log.Printf("[classify] stream error (defaulting to task): %s", delta.Text)
			return IntentTask
		}
	}

	return parseIntent(text.String())
}

// parseIntent extracts the intent from the LLM's JSON response.
// Returns IntentTask on any parse failure.
func parseIntent(raw string) Intent {
	raw = strings.TrimSpace(raw)

	var result struct {
		Intent string `json:"intent"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		log.Printf("[classify] parse error (defaulting to task): %v — raw: %q", err, raw)
		return IntentTask
	}

	switch Intent(result.Intent) {
	case IntentQuestion:
		return IntentQuestion
	case IntentTask:
		return IntentTask
	default:
		log.Printf("[classify] unknown intent %q (defaulting to task)", result.Intent)
		return IntentTask
	}
}
