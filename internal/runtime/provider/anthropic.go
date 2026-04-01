// Package provider implements LLM provider adapters.
package provider

import (
	"context"
	"encoding/json"
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

// Chat creates a streaming messages request and returns a channel of deltas.
func (p *AnthropicProvider) Chat(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	system := make([]anthropic.TextBlockParam, len(req.System))
	for i, block := range req.System {
		textBlock := anthropic.TextBlockParam{
			Text: block.Text,
		}

		if block.CacheControl != nil {
			textBlock.CacheControl = anthropic.NewCacheControlEphemeralParam()
		}

		system[i] = textBlock
	}

	messages := make([]anthropic.MessageParam, len(req.Messages))
	for i, msg := range req.Messages {
		content := make([]anthropic.ContentBlockParamUnion, len(msg.Content))
		for j, block := range msg.Content {
			switch block.Type {
			case "text":
				content[j] = anthropic.NewTextBlock(block.Text)
			case "tool_use":
				content[j] = anthropic.NewToolUseBlock(block.ID, block.Input, block.Name)
			case "tool_result":
				resultJSON, err := json.Marshal(block.Content)
				if err != nil {
					return nil, fmt.Errorf("marshal tool result: %w", err)
				}
				content[j] = anthropic.NewToolResultBlock(block.ToolUseID, string(resultJSON), false)
			}
		}

		messages[i] = anthropic.NewUserMessage(content...)
		if msg.Role == "assistant" {
			messages[i] = anthropic.NewAssistantMessage(content...)
		}
	}

	tools := make([]anthropic.ToolUnionParam, len(req.Tools))
	for i, tool := range req.Tools {
		schemaBytes, err := json.Marshal(tool.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("marshal tool input schema: %w", err)
		}

		var inputSchema anthropic.ToolInputSchemaParam
		if err := json.Unmarshal(schemaBytes, &inputSchema); err != nil {
			return nil, fmt.Errorf("unmarshal tool input schema: %w", err)
		}

		toolUnion := anthropic.ToolUnionParamOfTool(inputSchema, tool.Name)
		if tool.Description != "" {
			toolUnion.OfTool.Description = anthropic.Opt(tool.Description)
		}
		tools[i] = toolUnion
	}

	streamParams := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: int64(req.MaxTokens),
		Messages:  messages,
		System:    system,
	}

	if len(tools) > 0 {
		streamParams.Tools = tools
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
						InputTokens:  int(usage.InputTokens),
						OutputTokens: int(usage.OutputTokens),
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
			ch <- types.ChatDelta{
				Type: "error",
				Text: err.Error(),
			}
		}
	}()

	return ch, nil
}
