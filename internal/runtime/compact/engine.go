// Package compact implements conversation context compaction using LLM-based summarization.
package compact

import (
	"context"
	"fmt"
	"strings"

	"github.com/jelmersnoeck/forge/internal/types"
)

// ── Configuration ────────────────────────────────────────────

// Config controls compaction behavior.
type Config struct {
	TokenThreshold     int
	TargetTokens       int
	SummarizeModel     string
	MaxSummarizeTokens int
	Provider           types.LLMProvider
}

// DefaultConfig returns sensible defaults for compaction.
func DefaultConfig(provider types.LLMProvider) Config {
	return Config{
		TokenThreshold:     100_000,
		TargetTokens:       30_000,
		SummarizeModel:     "claude-3-5-haiku-20241022",
		MaxSummarizeTokens: 4096,
		Provider:           provider,
	}
}

// ── Compaction Engine ────────────────────────────────────────

// Engine handles conversation compaction.
type Engine struct {
	config Config
}

// NewEngine creates a new compaction engine.
func NewEngine(config Config) *Engine {
	return &Engine{config: config}
}

// ShouldCompact returns true if the conversation should be compacted.
func (e *Engine) ShouldCompact(estimatedTokens int) bool {
	return estimatedTokens >= e.config.TokenThreshold
}

// Compact summarizes old messages and returns a compacted conversation.
func (e *Engine) Compact(ctx context.Context, messages []types.ChatMessage) ([]types.ChatMessage, error) {
	if len(messages) == 0 {
		return messages, nil
	}

	totalTokens := e.estimateTokens(messages)
	if totalTokens < e.config.TokenThreshold {
		return messages, nil
	}

	keepCount := len(messages) / 3
	if keepCount < 5 {
		keepCount = 5
	}
	if keepCount >= len(messages) {
		return messages, nil
	}

	toSummarize := messages[:len(messages)-keepCount]
	toKeep := messages[len(messages)-keepCount:]

	summary, err := e.summarize(ctx, toSummarize)
	if err != nil {
		return nil, fmt.Errorf("generate summary: %w", err)
	}

	compacted := []types.ChatMessage{
		{
			Role: "user",
			Content: []types.ChatContentBlock{
				{
					Type: "text",
					Text: fmt.Sprintf("[Conversation summary - %d messages compressed]\n\n%s\n\n[End of summary. The conversation continues below.]", len(toSummarize), summary),
				},
			},
		},
	}
	compacted = append(compacted, toKeep...)

	return compacted, nil
}

// summarize calls the LLM to generate a conversation summary.
func (e *Engine) summarize(ctx context.Context, messages []types.ChatMessage) (string, error) {
	if len(messages) == 0 {
		return "", nil
	}

	stripped := stripImages(messages)

	systemPrompt := []types.SystemBlock{
		{
			Type: "text",
			Text: buildSummarizationPrompt(),
		},
	}

	req := types.ChatRequest{
		Model:     e.config.SummarizeModel,
		System:    systemPrompt,
		Messages:  stripped,
		Tools:     nil,
		MaxTokens: e.config.MaxSummarizeTokens,
		Stream:    false,
	}

	deltaChan, err := e.config.Provider.Chat(ctx, req)
	if err != nil {
		return "", fmt.Errorf("start summarization: %w", err)
	}

	var summary strings.Builder
	for delta := range deltaChan {
		if delta.Type == "text_delta" {
			summary.WriteString(delta.Text)
		}
	}

	result := summary.String()
	if result == "" {
		return "", fmt.Errorf("empty summary received")
	}

	return result, nil
}

// estimateTokens roughly estimates token count (4 chars per token).
func (e *Engine) estimateTokens(messages []types.ChatMessage) int {
	totalChars := 0
	for _, msg := range messages {
		for _, block := range msg.Content {
			if block.Type == "text" {
				totalChars += len(block.Text)
			}
		}
	}
	return totalChars / 4
}

// stripImages removes image blocks from messages (for summarization).
func stripImages(messages []types.ChatMessage) []types.ChatMessage {
	result := make([]types.ChatMessage, len(messages))
	for i, msg := range messages {
		newContent := []types.ChatContentBlock{}
		for _, block := range msg.Content {
			switch block.Type {
			case "text", "tool_use", "tool_result":
				newContent = append(newContent, block)
			case "image":
				newContent = append(newContent, types.ChatContentBlock{
					Type: "text",
					Text: "[image attachment]",
				})
			}
		}
		result[i] = types.ChatMessage{
			Role:    msg.Role,
			Content: newContent,
		}
	}
	return result
}

// buildSummarizationPrompt returns the system prompt for summarization.
func buildSummarizationPrompt() string {
	return `You are helping to compress a long conversation history by generating a concise summary.

Your task:
1. Read through the conversation messages provided
2. Extract the key information:
   - What tasks were discussed or completed
   - Important decisions or constraints
   - File changes, commands run, or tools used
   - Any ongoing context the assistant needs to continue effectively
3. Write a dense, factual summary in 2-4 paragraphs
4. Focus on information that would help continue the conversation, not narrative details

Format:
- Use clear, concise language
- List specific file names, commands, or tools when relevant  
- Omit pleasantries, greetings, and conversational fluff
- Use past tense ("The user asked...", "I created...")

Example good summary:
"""
The user requested a new authentication module for their Go web app. I created auth.go with JWT token generation, login/logout handlers, and middleware. Tests were added in auth_test.go covering happy paths and edge cases. The user then asked to integrate this with the existing user service, so I modified user_service.go to call the auth functions and updated the main.go router to use the auth middleware.

A bug was discovered where expired tokens weren't being rejected properly. I fixed the token validation logic and added a regression test. The user confirmed the fix works. They then requested rate limiting on the login endpoint to prevent brute force attacks. I implemented a simple in-memory rate limiter with a 5 requests per minute limit per IP address.
"""

Now summarize the conversation below.`
}
