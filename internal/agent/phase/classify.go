package phase

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode"

	"github.com/jelmersnoeck/forge/internal/types"
)

// Intent represents the classified user intent.
type Intent string

const (
	IntentQuestion Intent = "question"
	IntentTask     Intent = "task"
)

// classificationTimeout is the per-attempt timeout for classification.
// Kept tight: spec targets <500ms, but network jitter needs a buffer.
const classificationTimeout = 2 * time.Second

// classificationSystemPrompt is kept minimal to stay within ~200 input tokens.
const classificationSystemPrompt = `Classify the user's message as either a question or a task.

question: informational, exploratory, asking how something works, requesting an explanation.
  Examples: "how does the caching work?", "what files handle MCP?", "explain the session lifecycle"

task: actionable request to build, fix, change, implement, refactor, or modify something.
  Examples: "add a --verbose flag", "fix the nil pointer in worker.go", "implement retry logic"

Ambiguous cases (could be either) default to task:
  "the caching could be improved" → task
  "this error handling seems wrong" → task

Respond with ONLY a JSON object: {"intent": "question"} or {"intent": "task"}`

// maxClassifyPromptLen caps the user prompt sent to the classifier.
// ~1000 chars keeps us well within the ~200 input token budget.
const maxClassifyPromptLen = 1000

// maxStripInputLen caps input to stripCodeFences. Anything beyond this is
// clearly not a well-formed short JSON response and is returned as-is.
const maxStripInputLen = 4096

// ClassifyIntent uses a lightweight LLM call to determine whether the user's
// prompt is an informational question or an actionable task request.
// Returns (IntentTask, nil) for empty prompts.
// Returns (IntentTask, err) on classification failure (safe default).
// Tries each model in types.LightweightModels before giving up.
func ClassifyIntent(ctx context.Context, provider types.LLMProvider, prompt string) (Intent, error) {
	if strings.TrimSpace(prompt) == "" {
		return IntentTask, nil
	}

	classifyPrompt := truncateAtWordBoundary(prompt, maxClassifyPromptLen)

	var lastErr error
	for i, model := range types.LightweightModels {
		intent, err := classifyWithModel(ctx, provider, model, classifyPrompt)
		if err == nil {
			switch {
			case i > 0:
				log.Printf("[classify] succeeded on fallback model %s after %d failed attempt(s)", model, i)
			default:
				log.Printf("[classify] intent=%s model=%s", intent, model)
			}
			return intent, nil
		}
		lastErr = err
		log.Printf("[classify] model %s failed: %v", model, err)
	}

	log.Printf("[classify] all %d models failed — defaulting to task", len(types.LightweightModels))
	return IntentTask, fmt.Errorf("all models failed: %w", lastErr)
}

// classifyWithModel runs a single classification attempt against a specific model.
func classifyWithModel(ctx context.Context, provider types.LLMProvider, model, prompt string) (Intent, error) {
	classifyCtx, cancel := context.WithTimeout(ctx, classificationTimeout)
	defer cancel()

	req := types.ChatRequest{
		Model: model,
		System: []types.SystemBlock{
			{Type: "text", Text: classificationSystemPrompt},
		},
		Messages: []types.ChatMessage{
			{
				Role: "user",
				Content: []types.ChatContentBlock{
					{Type: "text", Text: prompt},
				},
			},
		},
		MaxTokens: 32,
		Stream:    true,
	}

	deltaChan, err := provider.Chat(classifyCtx, req)
	if err != nil {
		return IntentTask, fmt.Errorf("provider.Chat: %w", err)
	}

	// Drain deltas and collect text.
	var text strings.Builder
	for delta := range deltaChan {
		switch delta.Type {
		case "text_delta":
			text.WriteString(delta.Text)
		case "error":
			return IntentTask, fmt.Errorf("stream error: %s", delta.Text)
		}
	}

	intent, err := parseIntent(text.String())
	if err != nil {
		return IntentTask, err
	}
	return intent, nil
}

// truncateAtWordBoundary truncates s to at most maxLen runes, cutting at
// the last whitespace boundary to avoid splitting mid-word or mid-token.
// Uses rune-aware iteration so multi-byte UTF-8 (CJK, emoji) stays intact.
func truncateAtWordBoundary(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}

	// Walk backward from maxLen to find a whitespace boundary.
	cut := maxLen
	for cut > 0 && !unicode.IsSpace(runes[cut-1]) {
		cut--
	}

	// If the entire prefix is a single massive word, hard-cut at maxLen.
	if cut == 0 {
		cut = maxLen
	}

	return strings.TrimRightFunc(string(runes[:cut]), unicode.IsSpace) + "..."
}

// stripCodeFences removes markdown code fences that LLMs sometimes wrap around
// JSON output, e.g. ```json\n{...}\n``` -> {...}
//
// Only strips when the input is a single fenced block: opening ``` at the start
// and closing ``` at the end. If triple backticks appear in the middle (e.g.
// inside JSON content), the input is returned unchanged to avoid corrupting data.
// Inputs larger than maxStripInputLen are returned as-is.
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)

	if len(s) > maxStripInputLen {
		return s
	}

	if !strings.HasPrefix(s, "```") {
		return s
	}

	// Closing fence must be at the very end.
	if !strings.HasSuffix(s, "```") {
		// Opening fence but no closing fence — strip the opener and return.
		if idx := strings.Index(s, "\n"); idx != -1 {
			return strings.TrimSpace(s[idx+1:])
		}
		return strings.TrimPrefix(s, "```")
	}

	// Both opening and closing fences present. Find content between them.
	// Opening fence: everything up to (and including) the first newline.
	// Closing fence: the final ```.
	inner := s[3 : len(s)-3] // strip leading and trailing ```

	// Strip the language tag from the opening fence (e.g. "json\n").
	if idx := strings.Index(inner, "\n"); idx != -1 {
		inner = inner[idx+1:]
	}

	// Verify no stray ``` remain inside the content — if they do, the input
	// isn't a simple wrapping fence and we should leave it alone.
	if strings.Contains(inner, "```") {
		// Fall back to the original behavior: strip first line, strip last ```.
		// This handles the case where the content itself has backticks but
		// the overall structure is still open-fence / content / close-fence.
		//
		// Actually, just return original stripped content — we can't safely
		// determine which backticks are structural vs content.
		return s
	}

	return strings.TrimSpace(inner)
}

// parseIntent extracts the intent from the LLM's JSON response.
// Returns (IntentTask, err) on any parse failure.
func parseIntent(raw string) (Intent, error) {
	stripped := stripCodeFences(raw)

	// Log when code fences were stripped — helps diagnose model behavior.
	if stripped != strings.TrimSpace(raw) {
		log.Printf("[classify] stripped code fences from LLM response: %q -> %q", raw, stripped)
	}

	var result struct {
		Intent string `json:"intent"`
	}
	if err := json.Unmarshal([]byte(stripped), &result); err != nil {
		log.Printf("[classify] malformed LLM response (parse failed): %q", raw)
		return IntentTask, fmt.Errorf("parse error: %w — raw: %q", err, raw)
	}

	switch Intent(result.Intent) {
	case IntentQuestion:
		return IntentQuestion, nil
	case IntentTask:
		return IntentTask, nil
	case "":
		log.Printf("[classify] malformed LLM response (missing intent field): %q", raw)
		return IntentTask, fmt.Errorf("missing intent field in response: %q", raw)
	default:
		log.Printf("[classify] malformed LLM response (unknown intent %q): %q", result.Intent, raw)
		return IntentTask, fmt.Errorf("unknown intent %q", result.Intent)
	}
}
