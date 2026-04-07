package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

// writeSSE marshals a chunk and writes it as an SSE data line.
func writeSSE(w http.ResponseWriter, chunk oaiChunk) {
	b, _ := json.Marshal(chunk)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
}

// writeDone writes the SSE terminator.
func writeDone(w http.ResponseWriter) {
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

// strPtr returns a pointer to s. Because Go can't take &"literal".
func strPtr(s string) *string { return &s }

// collectDeltas drains a delta channel into a slice.
func collectDeltas(ch <-chan types.ChatDelta) []types.ChatDelta {
	var deltas []types.ChatDelta
	for d := range ch {
		deltas = append(deltas, d)
	}
	return deltas
}

func TestOpenAIChat(t *testing.T) {
	tests := map[string]struct {
		// handler writes SSE to the response
		handler func(w http.ResponseWriter, r *http.Request)
		// req is the ChatRequest sent to Chat
		req types.ChatRequest
		// check runs assertions on the collected deltas
		check func(r *require.Assertions, deltas []types.ChatDelta)
		// wantErr means Chat itself returns an error (not an error delta)
		wantErr bool
	}{
		"text streaming": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				// Troy Barnes narrating his journey to Greendale
				writeSSE(w, oaiChunk{
					ID: "chatcmpl-troy1",
					Choices: []oaiChoice{{
						Index: 0,
						Delta: oaiDelta{Content: strPtr("Troy Barnes ")},
					}},
				})
				writeSSE(w, oaiChunk{
					ID: "chatcmpl-troy1",
					Choices: []oaiChoice{{
						Index: 0,
						Delta: oaiDelta{Content: strPtr("is enrolling at Greendale!")},
					}},
				})
				writeSSE(w, oaiChunk{
					ID: "chatcmpl-troy1",
					Choices: []oaiChoice{{
						Index:        0,
						Delta:        oaiDelta{},
						FinishReason: strPtr("stop"),
					}},
				})
				writeDone(w)
			},
			req: types.ChatRequest{
				Model: "gpt-4.1",
				Messages: []types.ChatMessage{{
					Role:    "user",
					Content: []types.ChatContentBlock{{Type: "text", Text: "Tell me about Troy Barnes"}},
				}},
			},
			check: func(r *require.Assertions, deltas []types.ChatDelta) {
				// Should have 2 text deltas + 1 message_stop
				var texts []string
				for _, d := range deltas {
					if d.Type == "text_delta" {
						texts = append(texts, d.Text)
					}
				}
				r.Equal([]string{"Troy Barnes ", "is enrolling at Greendale!"}, texts)

				last := deltas[len(deltas)-1]
				r.Equal("message_stop", last.Type)
				r.Equal("end_turn", last.StopReason)
			},
		},

		"tool call streaming": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				// Tool call starts
				writeSSE(w, oaiChunk{
					ID: "chatcmpl-abed1",
					Choices: []oaiChoice{{
						Index: 0,
						Delta: oaiDelta{
							ToolCalls: []oaiDeltaToolCall{{
								Index: 0,
								ID:    "call_paintball",
								Type:  "function",
								Function: oaiDeltaFunctionCall{
									Name:      "EnrollAtGreendale",
									Arguments: "",
								},
							}},
						},
					}},
				})
				// Arguments streamed in chunks
				writeSSE(w, oaiChunk{
					ID: "chatcmpl-abed1",
					Choices: []oaiChoice{{
						Index: 0,
						Delta: oaiDelta{
							ToolCalls: []oaiDeltaToolCall{{
								Index:    0,
								Function: oaiDeltaFunctionCall{Arguments: `{"student"`},
							}},
						},
					}},
				})
				writeSSE(w, oaiChunk{
					ID: "chatcmpl-abed1",
					Choices: []oaiChoice{{
						Index: 0,
						Delta: oaiDelta{
							ToolCalls: []oaiDeltaToolCall{{
								Index:    0,
								Function: oaiDeltaFunctionCall{Arguments: `:"Abed Nadir"}`},
							}},
						},
					}},
				})
				// Finish with tool_calls reason
				writeSSE(w, oaiChunk{
					ID: "chatcmpl-abed1",
					Choices: []oaiChoice{{
						Index:        0,
						Delta:        oaiDelta{},
						FinishReason: strPtr("tool_calls"),
					}},
				})
				writeDone(w)
			},
			req: types.ChatRequest{
				Model: "gpt-4.1",
				Messages: []types.ChatMessage{{
					Role:    "user",
					Content: []types.ChatContentBlock{{Type: "text", Text: "Enroll Abed at Greendale"}},
				}},
				Tools: []types.ToolSchema{{
					Name:        "EnrollAtGreendale",
					Description: "Enrolls a student at Greendale Community College",
					InputSchema: map[string]any{"type": "object"},
				}},
			},
			check: func(r *require.Assertions, deltas []types.ChatDelta) {
				// Expect: tool_use_start, tool_use_delta x2, tool_use_end, message_stop
				typeSeq := make([]string, len(deltas))
				for i, d := range deltas {
					typeSeq[i] = d.Type
				}
				r.Equal([]string{
					"tool_use_start",
					"tool_use_delta",
					"tool_use_delta",
					"tool_use_end",
					"message_stop",
				}, typeSeq)

				// Verify tool_use_start fields
				r.Equal("call_paintball", deltas[0].ID)
				r.Equal("EnrollAtGreendale", deltas[0].Name)

				// Verify argument fragments
				r.Equal(`{"student"`, deltas[1].PartialJSON)
				r.Equal(`:"Abed Nadir"}`, deltas[2].PartialJSON)

				// Verify stop reason
				r.Equal("tool_use", deltas[4].StopReason)
			},
		},

		"usage tracking": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				writeSSE(w, oaiChunk{
					ID: "chatcmpl-chang1",
					Choices: []oaiChoice{{
						Index: 0,
						Delta: oaiDelta{Content: strPtr("Senor Chang says hi")},
					}},
				})
				// Final chunk with usage (choices empty, usage populated)
				writeSSE(w, oaiChunk{
					ID:      "chatcmpl-chang1",
					Choices: []oaiChoice{{Index: 0, Delta: oaiDelta{}, FinishReason: strPtr("stop")}},
					Usage: &oaiUsage{
						PromptTokens:     42,
						CompletionTokens: 17,
						TotalTokens:      59,
					},
				})
				writeDone(w)
			},
			req: types.ChatRequest{
				Model: "gpt-4.1",
				Messages: []types.ChatMessage{{
					Role:    "user",
					Content: []types.ChatContentBlock{{Type: "text", Text: "What does Senor Chang teach?"}},
				}},
			},
			check: func(r *require.Assertions, deltas []types.ChatDelta) {
				var usageDelta *types.ChatDelta
				for _, d := range deltas {
					if d.Type == "usage" {
						d := d
						usageDelta = &d
					}
				}
				r.NotNil(usageDelta, "should have a usage delta")
				r.Equal(42, usageDelta.Usage.InputTokens)
				r.Equal(17, usageDelta.Usage.OutputTokens)
			},
		},

		"API error response": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(oaiErrorResponse{
					Error: struct {
						Message string `json:"message"`
						Type    string `json:"type"`
					}{
						Message: "Rate limit exceeded. Dean Pelton says slow down.",
						Type:    "rate_limit_error",
					},
				})
			},
			req: types.ChatRequest{
				Model: "gpt-4.1",
				Messages: []types.ChatMessage{{
					Role:    "user",
					Content: []types.ChatContentBlock{{Type: "text", Text: "Spam from the Human Being mascot"}},
				}},
			},
			check: func(r *require.Assertions, deltas []types.ChatDelta) {
				r.Len(deltas, 1)
				r.Equal("error", deltas[0].Type)
				r.Equal(http.StatusTooManyRequests, deltas[0].StatusCode)
				r.Contains(deltas[0].Text, "Dean Pelton says slow down")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			srv := httptest.NewServer(http.HandlerFunc(tc.handler))
			defer srv.Close()

			p := NewOpenAI("sk-test-human-being")
			p.endpoint = srv.URL

			ch, err := p.Chat(context.Background(), tc.req)
			if tc.wantErr {
				r.Error(err)
				return
			}
			r.NoError(err)

			deltas := collectDeltas(ch)
			tc.check(r, deltas)
		})
	}
}

func TestBuildOpenAIRequest(t *testing.T) {
	tests := map[string]struct {
		req   types.ChatRequest
		check func(r *require.Assertions, oai oaiRequest)
	}{
		"request building": {
			req: types.ChatRequest{
				Model: "gpt-4.1",
				System: []types.SystemBlock{
					{Type: "text", Text: "You are the Dean of Greendale Community College."},
					{Type: "text", Text: "Always speak in a Dalmatian-obsessed manner."},
				},
				Messages: []types.ChatMessage{
					{
						Role: "user",
						Content: []types.ChatContentBlock{
							{Type: "text", Text: "Can Troy Barnes enroll?"},
						},
					},
					{
						Role: "assistant",
						Content: []types.ChatContentBlock{
							{Type: "text", Text: "Let me check the enrollment system."},
							{
								Type:  "tool_use",
								ID:    "toolu_enrollment",
								Name:  "EnrollAtGreendale",
								Input: map[string]any{"student": "Troy Barnes"},
							},
						},
					},
					{
						Role: "user",
						Content: []types.ChatContentBlock{
							{
								Type:      "tool_result",
								ToolUseID: "toolu_enrollment",
								Content: []types.ToolResultContent{
									{Type: "text", Text: "Enrollment confirmed. Go Human Beings!"},
								},
							},
						},
					},
				},
				Tools: []types.ToolSchema{
					{
						Name:        "EnrollAtGreendale",
						Description: "Enrolls a student at Greendale Community College",
						InputSchema: map[string]any{
							"type": "object",
							"properties": map[string]any{
								"student": map[string]any{"type": "string"},
							},
						},
					},
				},
				MaxTokens: 1024,
			},
			check: func(r *require.Assertions, oai oaiRequest) {
				r.Equal("gpt-4.1", oai.Model)
				r.True(oai.Stream)
				r.NotNil(oai.StreamOptions)
				r.True(oai.StreamOptions.IncludeUsage)
				r.Equal(1024, oai.MaxTokens)

				// 2 system + 1 user + 1 assistant (with tool_calls) + 1 tool = 5 messages
				r.Len(oai.Messages, 5)

				// System messages
				r.Equal("system", oai.Messages[0].Role)
				r.Equal("You are the Dean of Greendale Community College.", oai.Messages[0].Content)
				r.Equal("system", oai.Messages[1].Role)
				r.Contains(oai.Messages[1].Content, "Dalmatian")

				// User message
				r.Equal("user", oai.Messages[2].Role)
				r.Equal("Can Troy Barnes enroll?", oai.Messages[2].Content)

				// Assistant message with tool_calls
				r.Equal("assistant", oai.Messages[3].Role)
				r.Equal("Let me check the enrollment system.", oai.Messages[3].Content)
				r.Len(oai.Messages[3].ToolCalls, 1)
				r.Equal("toolu_enrollment", oai.Messages[3].ToolCalls[0].ID)
				r.Equal("function", oai.Messages[3].ToolCalls[0].Type)
				r.Equal("EnrollAtGreendale", oai.Messages[3].ToolCalls[0].Function.Name)
				r.Contains(oai.Messages[3].ToolCalls[0].Function.Arguments, "Troy Barnes")

				// Tool result → role:"tool" message
				r.Equal("tool", oai.Messages[4].Role)
				r.Equal("toolu_enrollment", oai.Messages[4].ToolCallID)
				r.Contains(oai.Messages[4].Content, "Enrollment confirmed")

				// Tools
				r.Len(oai.Tools, 1)
				r.Equal("function", oai.Tools[0].Type)
				r.Equal("EnrollAtGreendale", oai.Tools[0].Function.Name)
				r.Equal("Enrolls a student at Greendale Community College", oai.Tools[0].Function.Description)
				r.NotNil(oai.Tools[0].Function.Parameters)
			},
		},

		"empty tools": {
			req: types.ChatRequest{
				Model: "gpt-4.1",
				Messages: []types.ChatMessage{
					{
						Role: "user",
						Content: []types.ChatContentBlock{
							{Type: "text", Text: "Cool. Cool cool cool."},
						},
					},
				},
				// No tools
			},
			check: func(r *require.Assertions, oai oaiRequest) {
				r.Nil(oai.Tools, "no tools means no tools field in request")
				r.Len(oai.Messages, 1)
				r.Equal("user", oai.Messages[0].Role)
				r.Equal("Cool. Cool cool cool.", oai.Messages[0].Content)
			},
		},

		"default model": {
			req: types.ChatRequest{
				Messages: []types.ChatMessage{
					{
						Role: "user",
						Content: []types.ChatContentBlock{
							{Type: "text", Text: "Streets ahead"},
						},
					},
				},
			},
			check: func(r *require.Assertions, oai oaiRequest) {
				r.Equal(openAIDefaultModel, oai.Model)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			oai := buildOpenAIRequest(tc.req)
			tc.check(r, oai)
		})
	}
}

func TestConvertMessage(t *testing.T) {
	tests := map[string]struct {
		msg  types.ChatMessage
		want []oaiMessage
	}{
		"user text blocks concatenated": {
			msg: types.ChatMessage{
				Role: "user",
				Content: []types.ChatContentBlock{
					{Type: "text", Text: "I'm Jeff Winger."},
					{Type: "text", Text: "I need 12 credits."},
				},
			},
			want: []oaiMessage{{
				Role:    "user",
				Content: "I'm Jeff Winger.\nI need 12 credits.",
			}},
		},

		"assistant with text and tool use": {
			msg: types.ChatMessage{
				Role: "assistant",
				Content: []types.ChatContentBlock{
					{Type: "text", Text: "Let me look that up."},
					{
						Type:  "tool_use",
						ID:    "toolu_abc",
						Name:  "SearchStudyRoom",
						Input: map[string]any{"room": "F"},
					},
				},
			},
			want: []oaiMessage{{
				Role:    "assistant",
				Content: "Let me look that up.",
				ToolCalls: []oaiToolCall{{
					ID:   "toolu_abc",
					Type: "function",
					Function: oaiFunctionCall{
						Name:      "SearchStudyRoom",
						Arguments: `{"room":"F"}`,
					},
				}},
			}},
		},

		"tool results become tool messages": {
			msg: types.ChatMessage{
				Role: "user",
				Content: []types.ChatContentBlock{
					{
						Type:      "tool_result",
						ToolUseID: "toolu_abc",
						Content: []types.ToolResultContent{
							{Type: "text", Text: "Study room F is occupied by the study group"},
						},
					},
					{
						Type:      "tool_result",
						ToolUseID: "toolu_def",
						Content: []types.ToolResultContent{
							{Type: "text", Text: "Paintball arena is empty"},
						},
					},
				},
			},
			want: []oaiMessage{
				{Role: "tool", Content: "Study room F is occupied by the study group", ToolCallID: "toolu_abc"},
				{Role: "tool", Content: "Paintball arena is empty", ToolCallID: "toolu_def"},
			},
		},

		"image tool result becomes placeholder": {
			msg: types.ChatMessage{
				Role: "user",
				Content: []types.ChatContentBlock{
					{
						Type:      "tool_result",
						ToolUseID: "toolu_img",
						Content: []types.ToolResultContent{
							{Type: "image"},
							{Type: "text", Text: "The Dean's outfit"},
						},
					},
				},
			},
			want: []oaiMessage{
				{Role: "tool", Content: "[image]\nThe Dean's outfit", ToolCallID: "toolu_img"},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got := convertMessage(tc.msg)
			r.Equal(len(tc.want), len(got))
			for i := range tc.want {
				r.Equal(tc.want[i].Role, got[i].Role)
				r.Equal(tc.want[i].Content, got[i].Content)
				r.Equal(tc.want[i].ToolCallID, got[i].ToolCallID)
				r.Equal(len(tc.want[i].ToolCalls), len(got[i].ToolCalls))
				for j := range tc.want[i].ToolCalls {
					r.Equal(tc.want[i].ToolCalls[j].ID, got[i].ToolCalls[j].ID)
					r.Equal(tc.want[i].ToolCalls[j].Type, got[i].ToolCalls[j].Type)
					r.Equal(tc.want[i].ToolCalls[j].Function.Name, got[i].ToolCalls[j].Function.Name)
					r.Equal(tc.want[i].ToolCalls[j].Function.Arguments, got[i].ToolCalls[j].Function.Arguments)
				}
			}
		})
	}
}

func TestOpenAIRequestHeaders(t *testing.T) {
	r := require.New(t)

	var capturedAuth string
	var capturedContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		capturedAuth = req.Header.Get("Authorization")
		capturedContentType = req.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeDone(w)
	}))
	defer srv.Close()

	p := NewOpenAI("sk-dean-pelton-dalmatian-fetish")
	p.endpoint = srv.URL

	ch, err := p.Chat(context.Background(), types.ChatRequest{
		Model: "gpt-4.1",
		Messages: []types.ChatMessage{{
			Role:    "user",
			Content: []types.ChatContentBlock{{Type: "text", Text: "Pop pop!"}},
		}},
	})
	r.NoError(err)
	collectDeltas(ch)

	r.Equal("Bearer sk-dean-pelton-dalmatian-fetish", capturedAuth)
	r.Equal("application/json", capturedContentType)
}

func TestOpenAIRequestBody(t *testing.T) {
	r := require.New(t)

	var capturedBody oaiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewDecoder(req.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeDone(w)
	}))
	defer srv.Close()

	p := NewOpenAI("sk-test")
	p.endpoint = srv.URL

	ch, err := p.Chat(context.Background(), types.ChatRequest{
		Model:     "gpt-4.1",
		MaxTokens: 2048,
		Messages: []types.ChatMessage{{
			Role:    "user",
			Content: []types.ChatContentBlock{{Type: "text", Text: "Six seasons and a movie"}},
		}},
	})
	r.NoError(err)
	collectDeltas(ch)

	r.True(capturedBody.Stream)
	r.NotNil(capturedBody.StreamOptions)
	r.True(capturedBody.StreamOptions.IncludeUsage)
	r.Equal("gpt-4.1", capturedBody.Model)
	r.Equal(2048, capturedBody.MaxTokens)
}

func TestOpenAIMaxTokensFinishReason(t *testing.T) {
	r := require.New(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeSSE(w, oaiChunk{
			ID: "chatcmpl-britta1",
			Choices: []oaiChoice{{
				Index: 0,
				Delta: oaiDelta{Content: strPtr("Britta'd it because of max tok")},
			}},
		})
		writeSSE(w, oaiChunk{
			ID: "chatcmpl-britta1",
			Choices: []oaiChoice{{
				Index:        0,
				Delta:        oaiDelta{},
				FinishReason: strPtr("length"),
			}},
		})
		writeDone(w)
	}))
	defer srv.Close()

	p := NewOpenAI("sk-test")
	p.endpoint = srv.URL

	ch, err := p.Chat(context.Background(), types.ChatRequest{
		Messages: []types.ChatMessage{{
			Role:    "user",
			Content: []types.ChatContentBlock{{Type: "text", Text: "Write a long essay about Britta Perry"}},
		}},
	})
	r.NoError(err)

	deltas := collectDeltas(ch)
	last := deltas[len(deltas)-1]
	r.Equal("message_stop", last.Type)
	r.Equal("max_tokens", last.StopReason)
}

func TestOpenAIErrorResponseNoBody(t *testing.T) {
	r := require.New(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		// No JSON body — just a raw 500
		_, _ = fmt.Fprint(w, "the darkest timeline")
	}))
	defer srv.Close()

	p := NewOpenAI("sk-test")
	p.endpoint = srv.URL

	ch, err := p.Chat(context.Background(), types.ChatRequest{
		Messages: []types.ChatMessage{{
			Role:    "user",
			Content: []types.ChatContentBlock{{Type: "text", Text: "Evil Abed wants in"}},
		}},
	})
	r.NoError(err)

	deltas := collectDeltas(ch)
	r.Len(deltas, 1)
	r.Equal("error", deltas[0].Type)
	r.Equal(http.StatusInternalServerError, deltas[0].StatusCode)
	// Falls back to generic message since body isn't valid JSON
	r.True(strings.Contains(deltas[0].Text, "500"))
}
