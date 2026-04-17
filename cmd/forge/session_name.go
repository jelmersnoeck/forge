package main

import (
	"context"
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
// using env-var auto-detection. Returns nil when no provider is available.
func newLightweightProvider() types.LLMProvider {
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		return provider.NewAnthropic(key)
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		return provider.NewOpenAI(key)
	}
	if _, err := exec.LookPath("claude"); err == nil {
		return provider.NewClaudeCLI()
	}
	return nil
}

// sessionNameTimeout caps the LLM call for slug generation.
const sessionNameTimeout = 3 * time.Second

// sessionNamePromptPrefix is prepended to the user's prompt for slug generation.
const sessionNamePromptPrefix = "Generate a 2-4 word kebab-case slug summarizing this task. " +
	"Reply with ONLY the slug, nothing else. Examples: fix-auth-timeout, " +
	"add-mcp-support, refactor-session-loop\n\n"

// generateSessionName uses an LLM provider to create a kebab-case slug
// summarizing the prompt. Falls back to a random adjective-noun pair when
// provider is nil or the call fails.
func generateSessionName(provider types.LLMProvider, prompt string) string {
	if prompt == "" || provider == nil {
		return fallbackSessionName()
	}

	// Truncate long prompts to keep the call cheap.
	const maxPromptLen = 200
	if len(prompt) > maxPromptLen {
		prompt = prompt[:maxPromptLen] + "..."
	}

	ctx, cancel := context.WithTimeout(context.Background(), sessionNameTimeout)
	defer cancel()

	req := types.ChatRequest{
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

	deltaChan, err := provider.Chat(ctx, req)
	if err != nil {
		return fallbackSessionName()
	}

	var text strings.Builder
	for delta := range deltaChan {
		switch delta.Type {
		case "text_delta":
			text.WriteString(delta.Text)
		case "error":
			return fallbackSessionName()
		}
	}

	slug := sanitizeSlug(text.String())
	if slug == "" {
		return fallbackSessionName()
	}
	return slug
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
