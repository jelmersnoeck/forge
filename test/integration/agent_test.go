//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/runtime/provider"
	"github.com/jelmersnoeck/forge/internal/tools"
	"github.com/jelmersnoeck/forge/internal/types"
)

// TestAgentHTTPServer tests the agent's HTTP endpoints with a real ConversationLoop.
func TestAgentHTTPServer(t *testing.T) {
	tests := map[string]struct {
		message      types.InboundMessage
		wantStatus   int
		wantEventSeq []string // expected event types in order
	}{
		"simple message triggers thinking": {
			message: types.InboundMessage{
				Text:      "What is 2+2?",
				User:      "Troy Barnes",
				Source:    "greendale",
				SessionID: "calc-session",
			},
			wantStatus: http.StatusAccepted,
			wantEventSeq: []string{
				"thinking", // agent starts thinking
			},
		},
		"empty message rejected": {
			message: types.InboundMessage{
				Text:      "",
				User:      "Jeff Winger",
				SessionID: "empty-session",
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Create a mock LLM provider that returns canned responses
			mockProvider := &mockLLMProvider{
				responses: []mockResponse{
					{
						content: "The answer is 4.",
						usage: types.TokenUsage{
							InputTokens:  10,
							OutputTokens: 5,
						},
					},
				},
			}

			hub := agent.NewHub()
			registry := tools.NewDefaultRegistry()

			// Start agent server on random port
			srv, port := startTestAgent(t, hub, mockProvider, registry)
			defer srv.Close()

			// Send message
			msgBytes, err := json.Marshal(tc.message)
			r.NoError(err)

			resp, err := http.Post(
				fmt.Sprintf("http://localhost:%d/messages", port),
				"application/json",
				bytes.NewReader(msgBytes),
			)
			r.NoError(err)
			defer resp.Body.Close()
			r.Equal(tc.wantStatus, resp.StatusCode)

			if tc.wantStatus != http.StatusAccepted {
				return // no events for failed requests
			}

			// Subscribe to events
			eventResp, err := http.Get(fmt.Sprintf("http://localhost:%d/events", port))
			r.NoError(err)
			defer eventResp.Body.Close()

			r.Equal("text/event-stream", eventResp.Header.Get("Content-Type"))

			// Collect events
			scanner := bufio.NewScanner(eventResp.Body)
			var events []types.OutboundEvent
			deadline := time.After(5 * time.Second)

		eventLoop:
			for {
				select {
				case <-ctx.Done():
					t.Fatal("context cancelled waiting for events")
				case <-deadline:
					break eventLoop
				default:
				}

				if !scanner.Scan() {
					break
				}
				line := scanner.Text()
				if strings.HasPrefix(line, "data: ") {
					data := strings.TrimPrefix(line, "data: ")
					var evt types.OutboundEvent
					if err := json.Unmarshal([]byte(data), &evt); err == nil {
						events = append(events, evt)
						// Stop after we see a "done" event
						if evt.Type == "done" {
							break eventLoop
						}
					}
				}
			}

			// Verify we got expected event types
			r.GreaterOrEqual(len(events), len(tc.wantEventSeq), "should have at least expected events")
			for i, wantType := range tc.wantEventSeq {
				r.Equal(wantType, events[i].Type, "event %d type mismatch", i)
			}
		})
	}
}

// TestAgentInterrupt tests the interrupt endpoint.
func TestAgentInterrupt(t *testing.T) {
	r := require.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Mock provider that sleeps to simulate work
	mockProvider := &slowMockProvider{
		delay: 2 * time.Second,
	}

	hub := agent.NewHub()
	registry := tools.NewDefaultRegistry()

	srv, port := startTestAgent(t, hub, mockProvider, registry)
	defer srv.Close()

	// Send a message
	msg := types.InboundMessage{
		Text:      "Do something slow",
		User:      "Britta Perry",
		SessionID: "interrupt-test",
	}
	msgBytes, err := json.Marshal(msg)
	r.NoError(err)

	_, err = http.Post(
		fmt.Sprintf("http://localhost:%d/messages", port),
		"application/json",
		bytes.NewReader(msgBytes),
	)
	r.NoError(err)

	// Give it a moment to start processing
	time.Sleep(100 * time.Millisecond)

	// Send interrupt
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://localhost:%d/interrupt", port), nil)
	r.NoError(err)

	resp, err := http.DefaultClient.Do(req)
	r.NoError(err)
	defer resp.Body.Close()
	r.Equal(http.StatusOK, resp.StatusCode)

	// Verify we got an interrupted event
	eventResp, err := http.Get(fmt.Sprintf("http://localhost:%d/events", port))
	r.NoError(err)
	defer eventResp.Body.Close()

	scanner := bufio.NewScanner(eventResp.Body)
	var gotInterrupt bool
	deadline := time.After(3 * time.Second)

eventLoop:
	for {
		select {
		case <-deadline:
			break eventLoop
		default:
		}

		if !scanner.Scan() {
			break
		}
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			var evt types.OutboundEvent
			if err := json.Unmarshal([]byte(data), &evt); err == nil {
				if evt.Type == "interrupted" || evt.Type == "error" {
					gotInterrupt = true
					break eventLoop
				}
			}
		}
	}

	r.True(gotInterrupt, "should receive interrupt/error event")
}

// ── Test Helpers ──

func startTestAgent(t *testing.T, hub *agent.Hub, llm provider.LLMProvider, registry *tools.Registry) (*http.Server, int) {
	// Implementation will start the agent server and return server + port
	// This requires refactoring agent.Server to be testable
	t.Skip("requires agent.Server refactoring for testability")
	return nil, 0
}

// mockLLMProvider returns canned responses for testing.
type mockLLMProvider struct {
	responses []mockResponse
	callCount int
}

type mockResponse struct {
	content string
	usage   types.TokenUsage
}

func (m *mockLLMProvider) SendMessage(ctx context.Context, req provider.MessageRequest, handler provider.StreamHandler) error {
	if m.callCount >= len(m.responses) {
		return fmt.Errorf("no more mock responses")
	}

	resp := m.responses[m.callCount]
	m.callCount++

	// Simulate streaming
	handler.OnContent(resp.content)
	handler.OnDone(resp.usage)
	return nil
}

func (m *mockLLMProvider) Name() string {
	return "mock-provider"
}

// slowMockProvider simulates slow LLM responses for interrupt testing.
type slowMockProvider struct {
	delay time.Duration
}

func (s *slowMockProvider) SendMessage(ctx context.Context, req provider.MessageRequest, handler provider.StreamHandler) error {
	select {
	case <-time.After(s.delay):
		handler.OnContent("Done sleeping")
		handler.OnDone(types.TokenUsage{InputTokens: 1, OutputTokens: 1})
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *slowMockProvider) Name() string {
	return "slow-mock-provider"
}
