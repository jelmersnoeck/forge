package main

import (
	"context"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// generateSessionName calls Haiku to create a kebab-case slug summarizing the
// prompt. Falls back to a random adjective-noun pair on error/timeout.
func generateSessionName(prompt string) string {
	if prompt == "" {
		return fallbackSessionName()
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return fallbackSessionName()
	}

	// Truncate long prompts to keep the Haiku call cheap.
	const maxPromptLen = 200
	if len(prompt) > maxPromptLen {
		prompt = prompt[:maxPromptLen] + "..."
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	msg, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeHaiku4_5,
		MaxTokens: 32,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(
				anthropic.NewTextBlock(
					"Generate a 2-4 word kebab-case slug summarizing this task. " +
						"Reply with ONLY the slug, nothing else. Examples: fix-auth-timeout, " +
						"add-mcp-support, refactor-session-loop\n\n" + prompt,
				),
			),
		},
	})
	if err != nil {
		return fallbackSessionName()
	}

	slug := extractSlug(msg)
	if slug == "" {
		return fallbackSessionName()
	}
	return slug
}

// extractSlug pulls a clean kebab-case slug from the Haiku response.
func extractSlug(msg *anthropic.Message) string {
	for _, block := range msg.Content {
		if block.Type != "text" {
			continue
		}
		raw := strings.TrimSpace(block.Text)
		return sanitizeSlug(raw)
	}
	return ""
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
