package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jelmersnoeck/forge/internal/types"
)

const openAIDefaultModel = "gpt-4.1"
const openAIDefaultEndpoint = "https://api.openai.com/v1/chat/completions"

// OpenAIProvider implements types.LLMProvider using the OpenAI Chat Completions
// API with streaming SSE. Pure net/http — no SDK.
//
//	┌─────────┐   POST /v1/chat/completions   ┌──────────┐
//	│  forge   │ ─────── stream:true ────────▶ │  OpenAI  │
//	│ (client) │ ◀── SSE data: {chunk} ─────── │  (API)   │
//	└─────────┘     data: [DONE]               └──────────┘
type OpenAIProvider struct {
	apiKey   string
	endpoint string
	client   *http.Client
}

// NewOpenAI creates a new OpenAI provider.
func NewOpenAI(apiKey string) *OpenAIProvider {
	return &OpenAIProvider{
		apiKey:   apiKey,
		endpoint: openAIDefaultEndpoint,
		client:   &http.Client{},
	}
}

// ── OpenAI API request types ────────────────────────────────

type oaiRequest struct {
	Model         string            `json:"model"`
	Messages      []oaiMessage      `json:"messages"`
	Tools         []oaiTool         `json:"tools,omitempty"`
	MaxTokens     int               `json:"max_tokens,omitempty"`
	Stream        bool              `json:"stream"`
	StreamOptions *oaiStreamOptions `json:"stream_options,omitempty"`
}

type oaiStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type oaiMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content,omitempty"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type oaiToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function oaiFunctionCall `json:"function"`
}

type oaiFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaiTool struct {
	Type     string      `json:"type"`
	Function oaiFunction `json:"function"`
}

type oaiFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// ── OpenAI SSE chunk types ──────────────────────────────────

type oaiChunk struct {
	ID      string      `json:"id"`
	Choices []oaiChoice `json:"choices"`
	Usage   *oaiUsage   `json:"usage,omitempty"`
}

type oaiChoice struct {
	Index        int      `json:"index"`
	Delta        oaiDelta `json:"delta"`
	FinishReason *string  `json:"finish_reason"`
}

type oaiDelta struct {
	Role      string             `json:"role,omitempty"`
	Content   *string            `json:"content,omitempty"`
	ToolCalls []oaiDeltaToolCall `json:"tool_calls,omitempty"`
}

type oaiDeltaToolCall struct {
	Index    int                  `json:"index"`
	ID       string               `json:"id,omitempty"`
	Type     string               `json:"type,omitempty"`
	Function oaiDeltaFunctionCall `json:"function"`
}

type oaiDeltaFunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type oaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type oaiErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// ── Request building ────────────────────────────────────────

// buildOpenAIRequest converts forge ChatRequest → OpenAI API request.
//
// Message format mapping (Anthropic → OpenAI):
//
//	SystemBlock[]       → role:"system" messages (one per block)
//	text blocks         → concatenated into role:"user"/"assistant" content string
//	tool_use blocks     → role:"assistant" with tool_calls array
//	tool_result blocks  → role:"tool" messages (one per block)
func buildOpenAIRequest(req types.ChatRequest) oaiRequest {
	model := req.Model
	if model == "" {
		model = openAIDefaultModel
	}

	var msgs []oaiMessage

	// System blocks become system messages.
	for _, block := range req.System {
		msgs = append(msgs, oaiMessage{
			Role:    "system",
			Content: block.Text,
		})
	}

	// Convert chat messages.
	for _, msg := range req.Messages {
		msgs = append(msgs, convertMessage(msg)...)
	}

	var tools []oaiTool
	for _, t := range req.Tools {
		tools = append(tools, oaiTool{
			Type: "function",
			Function: oaiFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	return oaiRequest{
		Model:     model,
		Messages:  msgs,
		Tools:     tools,
		MaxTokens: req.MaxTokens,
		Stream:    true,
		StreamOptions: &oaiStreamOptions{
			IncludeUsage: true,
		},
	}
}

// convertMessage converts a single ChatMessage into one or more OpenAI messages.
//
// Anthropic packs text, tool_use, and tool_result into a single message's content
// blocks. OpenAI wants them as separate messages:
//
//	text blocks       → single message with concatenated content
//	tool_use blocks   → assistant message with tool_calls[]
//	tool_result block → role:"tool" message per block
func convertMessage(msg types.ChatMessage) []oaiMessage {
	var result []oaiMessage

	// Collect text blocks and tool_use blocks from this message.
	var textParts []string
	var toolCalls []oaiToolCall
	var toolResults []oaiMessage

	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)

		case "tool_use":
			inputJSON, _ := json.Marshal(block.Input)
			toolCalls = append(toolCalls, oaiToolCall{
				ID:   block.ID,
				Type: "function",
				Function: oaiFunctionCall{
					Name:      block.Name,
					Arguments: string(inputJSON),
				},
			})

		case "tool_result":
			// Each tool_result becomes its own role:"tool" message.
			content := toolResultToString(block.Content)
			toolResults = append(toolResults, oaiMessage{
				Role:       "tool",
				Content:    content,
				ToolCallID: block.ToolUseID,
			})
		}
	}

	// Emit assistant message: may have text, tool_calls, or both.
	switch msg.Role {
	case "assistant":
		m := oaiMessage{Role: "assistant"}
		if len(textParts) > 0 {
			m.Content = strings.Join(textParts, "\n")
		}
		if len(toolCalls) > 0 {
			m.ToolCalls = toolCalls
		}
		result = append(result, m)

	case "user":
		// User text blocks become a single user message.
		if len(textParts) > 0 {
			result = append(result, oaiMessage{
				Role:    "user",
				Content: strings.Join(textParts, "\n"),
			})
		}
		// Tool results come after.
		result = append(result, toolResults...)
	}

	return result
}

// toolResultToString flattens ToolResultContent blocks into a string.
func toolResultToString(content []types.ToolResultContent) string {
	var parts []string
	for _, c := range content {
		switch c.Type {
		case "text":
			parts = append(parts, c.Text)
		case "image":
			parts = append(parts, "[image]")
		}
	}
	return strings.Join(parts, "\n")
}

// ── Streaming ───────────────────────────────────────────────

// Chat implements types.LLMProvider. Sends a streaming request to OpenAI and
// returns a channel of ChatDelta events.
func (p *OpenAIProvider) Chat(ctx context.Context, req types.ChatRequest) (<-chan types.ChatDelta, error) {
	oaiReq := buildOpenAIRequest(req)

	body, err := json.Marshal(oaiReq)
	if err != nil {
		return nil, fmt.Errorf("marshal openai request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		respBody, _ := io.ReadAll(resp.Body)

		errMsg := fmt.Sprintf("OpenAI API error (HTTP %d)", resp.StatusCode)
		var oaiErr oaiErrorResponse
		if json.Unmarshal(respBody, &oaiErr) == nil && oaiErr.Error.Message != "" {
			errMsg = oaiErr.Error.Message
		}

		ch := make(chan types.ChatDelta, 1)
		ch <- types.ChatDelta{
			Type:       "error",
			Text:       errMsg,
			StatusCode: resp.StatusCode,
		}
		close(ch)
		return ch, nil
	}

	ch := make(chan types.ChatDelta, 16)
	go p.streamResponse(ctx, resp.Body, ch)
	return ch, nil
}

// activeToolCall tracks state for a tool call being streamed incrementally.
type activeToolCall struct {
	id   string
	name string
}

// streamResponse reads SSE lines from the response body and emits ChatDelta
// events. Owns resp body lifetime.
func (p *OpenAIProvider) streamResponse(ctx context.Context, body io.ReadCloser, ch chan<- types.ChatDelta) {
	defer close(ch)
	defer func() { _ = body.Close() }()

	scanner := bufio.NewScanner(body)
	// OpenAI chunks can be large when tools have big arguments.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// Track active tool calls by index (OpenAI can stream multiple in parallel).
	activeTools := map[int]*activeToolCall{}

	for scanner.Scan() {
		// Check context cancellation between lines.
		select {
		case <-ctx.Done():
			ch <- types.ChatDelta{
				Type: "error",
				Text: ctx.Err().Error(),
			}
			return
		default:
		}

		line := scanner.Text()

		// SSE format: "data: {json}" or "data: [DONE]"
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		if data == "[DONE]" {
			return
		}

		var chunk oaiChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			ch <- types.ChatDelta{
				Type: "error",
				Text: fmt.Sprintf("unmarshal chunk: %s", err),
			}
			return
		}

		// Usage comes in the final chunk (stream_options.include_usage).
		if chunk.Usage != nil {
			ch <- types.ChatDelta{
				Type: "usage",
				Usage: &types.TokenUsage{
					InputTokens:  chunk.Usage.PromptTokens,
					OutputTokens: chunk.Usage.CompletionTokens,
				},
			}
		}

		for _, choice := range chunk.Choices {
			delta := choice.Delta

			// Text content.
			if delta.Content != nil && *delta.Content != "" {
				ch <- types.ChatDelta{
					Type: "text_delta",
					Text: *delta.Content,
				}
			}

			// Tool calls — streamed incrementally by index.
			for _, tc := range delta.ToolCalls {
				_, exists := activeTools[tc.Index]

				if !exists {
					// New tool call starting.
					activeTools[tc.Index] = &activeToolCall{
						id:   tc.ID,
						name: tc.Function.Name,
					}

					ch <- types.ChatDelta{
						Type: "tool_use_start",
						ID:   tc.ID,
						Name: tc.Function.Name,
					}
				}

				// Argument fragments.
				if tc.Function.Arguments != "" {
					ch <- types.ChatDelta{
						Type:        "tool_use_delta",
						PartialJSON: tc.Function.Arguments,
					}
				}
			}

			// Finish reason signals end of this choice.
			if choice.FinishReason != nil {
				switch *choice.FinishReason {
				case "tool_calls":
					// End all active tool calls, then stop with tool_use reason.
					for range activeTools {
						ch <- types.ChatDelta{
							Type: "tool_use_end",
						}
					}
					ch <- types.ChatDelta{
						Type:       "message_stop",
						StopReason: "tool_use",
					}

				case "stop":
					ch <- types.ChatDelta{
						Type:       "message_stop",
						StopReason: "end_turn",
					}

				case "length":
					ch <- types.ChatDelta{
						Type:       "message_stop",
						StopReason: "max_tokens",
					}
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- types.ChatDelta{
			Type: "error",
			Text: fmt.Sprintf("read stream: %s", err),
		}
	}
}
