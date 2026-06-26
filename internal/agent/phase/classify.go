package phase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/jelmersnoeck/forge/internal/spec"
	"github.com/jelmersnoeck/forge/internal/types"
)

// Intent represents the classified user intent.
type Intent string

const (
	IntentQuestion    Intent = "question"
	IntentTask        Intent = "task"
	IntentInvestigate Intent = "investigate"
	IntentReview      Intent = "review"
)

// TaskSize indicates the estimated scope of a task intent.
type TaskSize string

const (
	TaskSizeSmall    TaskSize = "small"    // typo fix, one-liner, config tweak
	TaskSizeStandard TaskSize = "standard" // normal feature, multi-file bugfix
	TaskSizeLarge    TaskSize = "large"    // new subsystem, major refactor
)

// Classification is the full result of classifying a user prompt.
type Classification struct {
	Intent    Intent
	Size      TaskSize // only meaningful when Intent == IntentTask
	SpecMatch string   // matched spec ID, empty if none; only for IntentTask
}

// classificationTimeout is the per-attempt timeout for classification.
// Must accommodate Claude CLI startup (~3-4s for tool/plugin/MCP init
// when --verbose is required) plus API response time.
const classificationTimeout = 6 * time.Second

// classificationSystemPromptTmpl is a template that accepts the spec index.
// The %s placeholder is replaced with the output of spec.FormatSpecIndex(specs)
// at runtime, or an empty string if no specs exist.
const classificationSystemPromptTmpl = `Classify the user's message.

Intents:
- question: informational, asking how something works, requesting an explanation.
- investigate: active exploration, debugging, root-cause analysis.
- review: asking to review existing changes, a diff, PR, or branch.
- task: actionable request to build, fix, change, implement, refactor.

Ambiguity rules:
- question vs investigate → investigate
- investigate vs task → investigate
- Mixed intent with change verb (fix, add, implement, refactor) → task
- "review my changes" / "review this PR" / "check the diff" → review

For task intent only, also determine:
- size: small (typo, one-liner, config change, single-file fix), standard (feature, bugfix, multi-file), large (new subsystem, cross-cutting refactor, major feature)
- spec_match: if the task clearly maps to an existing spec below, return its ID. Otherwise empty string.

%s

Respond with ONLY JSON: {"intent":"...","size":"...","spec_match":"..."}`

// maxClassifyPromptLen caps the user prompt sent to the classifier.
// ~1000 chars keeps us well within the ~200 input token budget.
const maxClassifyPromptLen = 1000

// maxStripInputLen caps input to stripCodeFences. The classifier requests
// MaxTokens=32, so valid responses are tiny. 4096 is generous headroom;
// anything beyond is clearly not a well-formed response and is returned as-is.
const maxStripInputLen = 4096

// Compile-time assertion: maxStripInputLen must be positive and bounded.
// Prevents accidental edits from setting it to zero or something absurd.
var _ [maxStripInputLen - 1]struct{}     // fails if <= 0
var _ [1<<20 - maxStripInputLen]struct{} // fails if > 1 MiB

// ClassifyIntent uses a lightweight LLM call to classify the user's prompt
// as question, investigate, review, or task.
// Returns (IntentTask, nil) for empty prompts.
// Returns (IntentTask, err) on classification failure (safe default).
// Tries each model in types.LightweightModels before giving up.
// Preserved for backward compatibility — delegates to Classify.
func ClassifyIntent(ctx context.Context, provider types.LLMProvider, prompt string) (Intent, error) {
	c, err := Classify(ctx, provider, prompt, nil)
	return c.Intent, err
}

// Classify uses a lightweight LLM call to classify the user's prompt,
// returning intent, task size, and an optional spec match.
// The specs parameter is used to populate the system prompt with the spec
// index so the classifier can match tasks to existing specs.
// Returns (Classification{IntentTask, TaskSizeStandard, ""}, nil) for empty prompts.
// Returns (Classification{IntentTask, TaskSizeStandard, ""}, err) on failure (safe default).
func Classify(ctx context.Context, provider types.LLMProvider, prompt string, specs []types.SpecEntry) (Classification, error) {
	defaultClassification := Classification{Intent: IntentTask, Size: TaskSizeStandard}

	if strings.TrimSpace(prompt) == "" {
		return defaultClassification, nil
	}

	classifyPrompt := truncateAtWordBoundary(prompt, maxClassifyPromptLen)
	systemPrompt := buildClassificationPrompt(specs)

	var lastErr error
	for i, model := range types.LightweightModels {
		c, err := classifyFullWithModel(ctx, provider, model, systemPrompt, classifyPrompt, specs)
		if err == nil {
			switch {
			case i > 0:
				slog.Info("classify: succeeded on fallback model",
					"model", model, "failed_attempts", i)
			default:
				slog.Debug("classify: classified",
					"intent", c.Intent, "size", c.Size,
					"spec_match", c.SpecMatch, "model", model)
			}
			return c, nil
		}
		lastErr = err
		slog.Warn("classify: model failed",
			"model", model, "error", err)
	}

	slog.Error("classify: all models failed, defaulting to task/standard",
		"models_tried", len(types.LightweightModels))
	if lastErr == nil {
		return defaultClassification, fmt.Errorf("all models failed (no models configured)")
	}
	return defaultClassification, fmt.Errorf("all models failed: %w", lastErr)
}

// buildClassificationPrompt constructs the system prompt with optional spec index.
func buildClassificationPrompt(specs []types.SpecEntry) string {
	specIndex := spec.FormatSpecIndex(specs)
	return fmt.Sprintf(classificationSystemPromptTmpl, specIndex)
}

// classifyFullWithModel runs a single classification attempt against a specific model,
// returning the full Classification.
func classifyFullWithModel(ctx context.Context, provider types.LLMProvider, model, systemPrompt, prompt string, specs []types.SpecEntry) (Classification, error) {
	defaultClassification := Classification{Intent: IntentTask, Size: TaskSizeStandard}

	classifyCtx, cancel := context.WithTimeout(ctx, classificationTimeout)
	defer cancel()

	req := types.ChatRequest{
		Model: model,
		System: []types.SystemBlock{
			{Type: "text", Text: systemPrompt},
		},
		Messages: []types.ChatMessage{
			{
				Role: "user",
				Content: []types.ChatContentBlock{
					{Type: "text", Text: prompt},
				},
			},
		},
		MaxTokens: 64,
		Stream:    true,
	}

	deltaChan, err := provider.Chat(classifyCtx, req)
	if err != nil {
		return defaultClassification, fmt.Errorf("provider.Chat: %w", err)
	}

	// Drain deltas and collect text.
	var text strings.Builder
	for delta := range deltaChan {
		switch delta.Type {
		case "text_delta":
			text.WriteString(delta.Text)
		case "error":
			return defaultClassification, fmt.Errorf("stream error: %s", delta.Text)
		}
	}

	c, err := parseClassification(text.String(), specs)
	if err != nil {
		return defaultClassification, err
	}
	return c, nil
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

	// Verify no stray ``` remain inside the content. If they do, we can't
	// safely distinguish structural fences from content — return unchanged.
	if strings.Contains(inner, "```") {
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
		slog.Info("classify: stripped code fences from LLM response",
			"raw", raw, "stripped", stripped)
	}

	var result struct {
		Intent string `json:"intent"`
	}
	if err := json.Unmarshal([]byte(stripped), &result); err != nil {
		slog.Error("classify: malformed LLM response",
			"reason", "parse_failed", "raw", raw, "error", err)
		return IntentTask, fmt.Errorf("parse error: %w — raw: %q", err, raw)
	}

	switch Intent(result.Intent) {
	case IntentQuestion:
		return IntentQuestion, nil
	case IntentInvestigate:
		return IntentInvestigate, nil
	case IntentTask:
		return IntentTask, nil
	case IntentReview:
		return IntentReview, nil
	case "":
		slog.Error("classify: malformed LLM response",
			"reason", "missing_intent_field", "raw", raw)
		return IntentTask, fmt.Errorf("missing intent field in response: %q", raw)
	default:
		slog.Error("classify: malformed LLM response",
			"reason", "unknown_intent", "intent", result.Intent, "raw", raw)
		return IntentTask, fmt.Errorf("unknown intent %q", result.Intent)
	}
}

// parseClassification extracts the full classification from the LLM's JSON response.
// Returns (Classification{IntentTask, TaskSizeStandard, ""}, err) on parse failure.
func parseClassification(raw string, specs []types.SpecEntry) (Classification, error) {
	defaultClassification := Classification{Intent: IntentTask, Size: TaskSizeStandard}

	stripped := stripCodeFences(raw)

	if stripped != strings.TrimSpace(raw) {
		slog.Info("classify: stripped code fences from LLM response",
			"raw", raw, "stripped", stripped)
	}

	var result struct {
		Intent    string `json:"intent"`
		Size      string `json:"size"`
		SpecMatch string `json:"spec_match"`
	}
	if err := json.Unmarshal([]byte(stripped), &result); err != nil {
		slog.Error("classify: malformed LLM response",
			"reason", "parse_failed", "raw", raw, "error", err)
		return defaultClassification, fmt.Errorf("parse error: %w — raw: %q", err, raw)
	}

	// Validate intent (4 values; unknown → IntentTask).
	var intent Intent
	switch Intent(result.Intent) {
	case IntentQuestion:
		intent = IntentQuestion
	case IntentInvestigate:
		intent = IntentInvestigate
	case IntentReview:
		intent = IntentReview
	case IntentTask:
		intent = IntentTask
	case "":
		slog.Error("classify: malformed LLM response",
			"reason", "missing_intent_field", "raw", raw)
		return defaultClassification, fmt.Errorf("missing intent field in response: %q", raw)
	default:
		slog.Warn("classify: unknown intent, defaulting to task",
			"intent", result.Intent, "raw", raw)
		intent = IntentTask
	}

	c := Classification{Intent: intent}

	// For non-task intents, ignore size and spec_match.
	if intent != IntentTask {
		return c, nil
	}

	// Validate size for task intents.
	switch TaskSize(result.Size) {
	case TaskSizeSmall:
		c.Size = TaskSizeSmall
	case TaskSizeLarge:
		c.Size = TaskSizeLarge
	case TaskSizeStandard:
		c.Size = TaskSizeStandard
	default:
		// Unknown or empty → standard.
		c.Size = TaskSizeStandard
	}

	// Validate spec_match against provided specs.
	if result.SpecMatch != "" {
		valid := false
		for _, s := range specs {
			if s.ID == result.SpecMatch {
				valid = true
				break
			}
		}
		if valid {
			c.SpecMatch = result.SpecMatch
		} else {
			slog.Info("classify: spec_match not found in provided specs, clearing",
				"spec_match", result.SpecMatch)
		}
	}

	return c, nil
}
