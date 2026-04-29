package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/runtime/provider"
	"github.com/jelmersnoeck/forge/internal/types"
)

// newLightweightProvider creates a provider for cheap LLM calls (session naming)
// using env-var auto-detection with a fixed priority order:
// Anthropic > OpenAI > Claude CLI. Returns nil when no provider is available.
//
// Intentionally does not cache: this runs once at session start, so the env
// lookups are negligible and caching would complicate test isolation.
func newLightweightProvider() types.LLMProvider {
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		log.Printf("[session-name] using Anthropic provider")
		return provider.NewAnthropic(key)
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		log.Printf("[session-name] using OpenAI provider")
		return provider.NewOpenAI(key)
	}
	if path, err := exec.LookPath("claude"); err == nil {
		// LookPath already checks executability on Unix (os.Stat + mode bits),
		// but verify we can stat the resolved path to catch broken symlinks.
		if _, statErr := os.Stat(path); statErr == nil {
			log.Printf("[session-name] using Claude CLI provider (%s)", path)
			return provider.NewClaudeCLI()
		}
		log.Printf("[session-name] claude found at %s but not accessible: %v", path, err)
	}
	log.Printf("[session-name] no LLM provider available — will use random names")
	return nil
}

// sessionNameTimeout caps the LLM call for slug generation.
const sessionNameTimeout = 3 * time.Second

// sessionNamePromptPrefix is prepended to the user's prompt for slug generation.
const sessionNamePromptPrefix = "Generate a 2-4 word kebab-case slug summarizing this task. " +
	"Reply with ONLY the slug, nothing else. " +
	"Examples: fix-auth-timeout, add-mcp-support, refactor-session-loop\n\n"

// maxStreamTextLen caps accumulated text from the LLM stream to prevent
// memory issues with malformed responses (expected: <50 bytes for a slug).
const maxStreamTextLen = 512

// drainTextDeltas collects text from a ChatDelta stream, returning the
// accumulated text and any stream error. Caps accumulated text at maxStreamTextLen.
func drainTextDeltas(deltaChan <-chan types.ChatDelta) (string, error) {
	var text strings.Builder
	for delta := range deltaChan {
		switch delta.Type {
		case "text_delta":
			if text.Len()+len(delta.Text) > maxStreamTextLen {
				remaining := maxStreamTextLen - text.Len()
				if remaining > 0 {
					text.WriteString(delta.Text[:remaining])
				}
			} else {
				text.WriteString(delta.Text)
			}
		case "error":
			return "", fmt.Errorf("stream error: %s", delta.Text)
		}
	}
	return text.String(), nil
}

// generateSessionName uses an LLM provider to create a kebab-case slug
// summarizing the prompt. Tries each model in types.LightweightModels,
// falling through on error. Falls back to a random adjective-noun pair
// when provider is nil, prompt is empty, or all models fail.
func generateSessionName(prov types.LLMProvider, prompt string) string {
	if prompt == "" || prov == nil {
		return fallbackSessionName()
	}

	// Truncate long prompts to keep the call cheap.
	const maxPromptLen = 200
	if len(prompt) > maxPromptLen {
		prompt = prompt[:maxPromptLen] + "..."
	}

	for _, model := range types.LightweightModels {
		if slug := trySessionNameModel(prov, model, prompt); slug != "" {
			return slug
		}
	}

	log.Printf("[session-name] all %d models failed — falling back to random name", len(types.LightweightModels))
	return fallbackSessionName()
}

// trySessionNameModel attempts a single model for slug generation.
// Returns the sanitized slug or "" on failure.
func trySessionNameModel(prov types.LLMProvider, model, prompt string) string {
	ctx, cancel := context.WithTimeout(context.Background(), sessionNameTimeout)
	defer cancel()

	req := types.ChatRequest{
		Model:  model,
		System: []types.SystemBlock{},
		Messages: []types.ChatMessage{
			{
				Role: "user",
				Content: []types.ChatContentBlock{
					{Type: "text", Text: sessionNamePromptPrefix + prompt},
				},
			},
		},
		MaxTokens: 32,
		Stream:    true,
	}

	deltaChan, err := prov.Chat(ctx, req)
	if err != nil {
		log.Printf("[session-name] model %s failed: %v", model, err)
		return ""
	}

	text, err := drainTextDeltas(deltaChan)
	if err != nil {
		log.Printf("[session-name] model %s: %v", model, err)
		return ""
	}

	return sanitizeSlug(text)
}

var slugRe = regexp.MustCompile(`[^a-z0-9-]`)

// sanitizeSlug normalizes arbitrary text into a valid kebab-case slug.
func sanitizeSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRe.ReplaceAllString(s, "-")

	// Collapse multiple dashes
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")

	// Truncate to reasonable length
	const maxSlugLen = 40
	if len(s) > maxSlugLen {
		s = s[:maxSlugLen]
		s = strings.TrimRight(s, "-")
	}

	return s
}

// fallbackSessionName generates a random readable name without API calls.
// Format: adjective-noun (e.g. "swift-falcon", "bright-mesa").
func fallbackSessionName() string {
	adjectives := []string{
		"swift", "bright", "calm", "bold", "keen",
		"quick", "warm", "cool", "sharp", "clear",
		"wild", "fair", "deep", "still", "fresh",
		"glad", "pure", "rare", "soft", "vast",
	}
	nouns := []string{
		"falcon", "mesa", "cedar", "spark", "ridge",
		"brook", "flame", "drift", "crane", "flint",
		"grove", "shore", "bloom", "frost", "stone",
		"trail", "reef", "dune", "peak", "vale",
	}

	//nolint:gosec // not crypto, just naming
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	adj := adjectives[r.Intn(len(adjectives))]
	noun := nouns[r.Intn(len(nouns))]
	return adj + "-" + noun
}
