// Package provider implements LLM provider adapters.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/jelmersnoeck/forge/internal/types"
)

// AnthropicProvider wraps the Anthropic SDK and implements types.LLMProvider.
type AnthropicProvider struct {
	client anthropic.Client
}

// NewAnthropic creates a new Anthropic provider.
func NewAnthropic(apiKey string) *AnthropicProvider {
	return &AnthropicProvider{
		client: anthropic.NewClient(option.WithAPIKey(apiKey)),
	}
}

// toCacheControl converts our CacheControl type to the Anthropic SDK param.
func toCacheControl(cc *types.CacheControl) anthropic.CacheControlEphemeralParam {
	param := anthropic.NewCacheControlEphemeralParam()
	switch cc.TTL {
	case "1h":
		param.TTL = anthropic.CacheControlEphemeralTTLTTL1h
	case "5m":
		param.TTL = anthropic.CacheControlEphemeralTTLTTL5m
	}
	return param
}

// maxCacheBreakpoints is the Anthropic API limit on cache_control blocks
// across the entire request (system + tools + messages combined).
const maxCacheBreakpoints = 4

// buildRequest converts a ChatRequest into Anthropic SDK params, enforcing
// the cache_control breakpoint limit.
//
//	Priority (highest to lowest):
//	  1. System blocks  -- most stable, biggest token savings
//	  2. Tool schemas   -- stable across turns
//	  3. Messages       -- changes every turn but caches growing history
func buildRequest(req types.ChatRequest) (anthropic.MessageNewParams, error) {
	cacheRemaining := maxCacheBreakpoints

	system := make([]anthropic.TextBlockParam, len(req.System))
	for i, block := range req.System {
		textBlock := anthropic.TextBlockParam{
			Text: block.Text,
		}
		if block.CacheControl != nil && cacheRemaining > 0 {
			textBlock.CacheControl = toCacheControl(block.CacheControl)
			cacheRemaining--
		}
		system[i] = textBlock
	}

	tools := make([]anthropic.ToolUnionParam, len(req.Tools))
	for i, tool := range req.Tools {
		schemaBytes, err := json.Marshal(tool.InputSchema)
		if err != nil {
			return anthropic.MessageNewParams{}, fmt.Errorf("marshal tool input schema: %w", err)
		}

		var inputSchema anthropic.ToolInputSchemaParam
		if err := json.Unmarshal(schemaBytes, &inputSchema); err != nil {
			return anthropic.MessageNewParams{}, fmt.Errorf("unmarshal tool input schema: %w", err)
		}

		toolUnion := anthropic.ToolUnionParamOfTool(inputSchema, tool.Name)
		if tool.Description != "" {
			toolUnion.OfTool.Description = anthropic.Opt(tool.Description)
		}

		if tool.CacheControl != nil && cacheRemaining > 0 {
			toolUnion.OfTool.CacheControl = toCacheControl(tool.CacheControl)
			cacheRemaining--
		}

		tools[i] = toolUnion
	}

	messages := make([]anthropic.MessageParam, len(req.Messages))
	for i, msg := range req.Messages {
		content := make([]anthropic.ContentBlockParamUnion, len(msg.Content))
		for j, block := range msg.Content {
			switch block.Type {
			case "text":
				textBlock := anthropic.TextBlockParam{
					Text: block.Text,
				}
				if block.CacheControl != nil && cacheRemaining > 0 {
					textBlock.CacheControl = toCacheControl(block.CacheControl)
					cacheRemaining--
				}
				content[j] = anthropic.ContentBlockParamUnion{
					OfText: &textBlock,
				}
			case "tool_use":
				toolUse := anthropic.NewToolUseBlock(block.ID, block.Input, block.Name)
				if block.CacheControl != nil && cacheRemaining > 0 && toolUse.OfToolUse != nil {
					toolUse.OfToolUse.CacheControl = toCacheControl(block.CacheControl)
					cacheRemaining--
				}
				content[j] = toolUse
			case "tool_result":
				resultJSON, err := json.Marshal(block.Content)
				if err != nil {
					return anthropic.MessageNewParams{}, fmt.Errorf("marshal tool result: %w", err)
				}
				toolResult := anthropic.NewToolResultBlock(block.ToolUseID, string(resultJSON), false)
				if block.CacheControl != nil && cacheRemaining > 0 && toolResult.OfToolResult != nil {
					toolResult.OfToolResult.CacheControl = toCacheControl(block.CacheControl)
					cacheRemaining--
				}
				content[j] = toolResult
			}
		}

		messages[i] = anthropic.NewUserMessage(content...)
		if msg.Role == "assistant" {
			messages[i] = anthropic.NewAssistantMessage(content...)
		}
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: int64(req.MaxTokens),
		Messages:  messages,
		System:    system,
	}

	if len(tools) > 0 {
		params.Tools = tools
	}

	return params, nil
}

// Chat creates a streaming messages request and returns a channel of deltas.
func (p *AnthropicProvider) Chat(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	streamParams, err := buildRequest(req)
	if err != nil {
		return nil, err
	}

	stream := p.client.Messages.NewStreaming(ctx, streamParams)

	ch := make(chan types.ChatDelta, 16)

	go func() {
		defer close(ch)
		defer stream.Close()

		var activeToolUse *types.ChatDelta

		for stream.Next() {
			event := stream.Current()

			switch event.Type {
			case "message_start":
				// Extract input token usage from the initial message.
				usage := event.Message.Usage
				ch <- types.ChatDelta{
					Type: "usage",
					Usage: &types.TokenUsage{
						InputTokens:         int(usage.InputTokens),
						OutputTokens:        int(usage.OutputTokens),
						CacheCreationTokens: int(usage.CacheCreationInputTokens),
						CacheReadTokens:     int(usage.CacheReadInputTokens),
					},
				}

			case "content_block_start":
				if event.ContentBlock.Type == "tool_use" {
					activeToolUse = &types.ChatDelta{
						Type: "tool_use_start",
						ID:   event.ContentBlock.ID,
						Name: event.ContentBlock.Name,
					}
					ch <- *activeToolUse
				}

			case "content_block_delta":
				deltaType := event.Delta.Type
				switch deltaType {
				case "text_delta":
					ch <- types.ChatDelta{
						Type: "text_delta",
						Text: event.Delta.Text,
					}

				case "input_json_delta":
					if activeToolUse != nil {
						ch <- types.ChatDelta{
							Type:        "tool_use_delta",
							PartialJSON: event.Delta.PartialJSON,
						}
					}
				}

			case "content_block_stop":
				if activeToolUse != nil {
					ch <- types.ChatDelta{
						Type: "tool_use_end",
					}
					activeToolUse = nil
				}

			case "message_delta":
				// Extract final output token count from message_delta.
				ch <- types.ChatDelta{
					Type: "usage",
					Usage: &types.TokenUsage{
						OutputTokens: int(event.Usage.OutputTokens),
					},
				}

			case "message_stop":
				stopReason := string(event.Message.StopReason)
				ch <- types.ChatDelta{
					Type:       "message_stop",
					StopReason: stopReason,
				}
			}
		}

		if err := stream.Err(); err != nil {
			statusCode := 0
			var apiErr *anthropic.Error
			if errors.As(err, &apiErr) {
				statusCode = apiErr.StatusCode
			}
			ch <- types.ChatDelta{
				Type:       "error",
				Text:       err.Error(),
				StatusCode: statusCode,
			}
		}
	}()

	return ch, nil
}
