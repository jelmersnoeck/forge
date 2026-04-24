package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/runtime/provider"
	"github.com/jelmersnoeck/forge/internal/types"
)

// newLightweightProvider creates a provider for cheap LLM calls (session naming).
// Priority order:
//  1. FORGE_PROVIDER env var (explicit override)
//  2. ~/.forge/config.toml [provider].default
//  3. Auto-detect: ANTHROPIC_API_KEY > OPENAI_API_KEY > Claude CLI
//
// Returns an error when a provider was resolved but credentials are unavailable,
// or when no provider could be detected at all.
func newLightweightProvider() (types.LLMProvider, error) {
	resolved := provider.ResolveProvider()

	if resolved.ConfigErr != nil {
		switch {
		case os.IsNotExist(resolved.ConfigErr):
			log.Printf("[session-name] config_status=not_found — falling back to auto-detect")
		default:
			return nil, fmt.Errorf("config corrupted or unreadable: %w", resolved.ConfigErr)
		}
	}

	if !resolved.Found {
		return nil, fmt.Errorf("no LLM provider available")
	}

	log.Printf("[session-name] using %s provider (via %s)", resolved.Name, resolved.Source)
	p := provider.FromName(resolved.Name)
	if p == nil {
		return nil, fmt.Errorf("provider %s resolved but credentials unavailable", resolved.Name)
	}
	return p, nil
}

// generateSlug creates a session slug from a prompt, handling provider
// creation and fallback internally. Callers don't need to touch the provider.
func generateSlug(prompt string) string {
	p, err := newLightweightProvider()
	if err != nil {
		log.Printf("[session-name] provider_error=%q — using random name", err)
		return fallbackSessionName()
	}
	return generateSessionName(p, prompt)
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
		log.Printf("[session-name] provider.Chat failed: %v — falling back to random name", err)
		return fallbackSessionName()
	}

	text, err := drainTextDeltas(deltaChan)
	if err != nil {
		log.Printf("[session-name] %v — falling back to random name", err)
		return fallbackSessionName()
	}

	slug := sanitizeSlug(text)
	if slug == "" {
		log.Printf("[session-name] LLM returned empty/unparseable response — falling back to random name")
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
