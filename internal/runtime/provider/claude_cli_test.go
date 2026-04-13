package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestNewClaudeCLI(t *testing.T) {
	r := require.New(t)
	p := NewClaudeCLI()
	r.NotNil(p)
	r.Empty(p.claudeSessionID)
}

func TestExtractPrompt(t *testing.T) {
	tests := map[string]struct {
		messages []types.ChatMessage
		want     string
	}{
		"single user message": {
			messages: []types.ChatMessage{
				{Role: "user", Content: []types.ChatContentBlock{
					{Type: "text", Text: "Hello from Greendale"},
				}},
			},
			want: "Hello from Greendale",
		},
		"multiple messages picks last user": {
			messages: []types.ChatMessage{
				{Role: "user", Content: []types.ChatContentBlock{
					{Type: "text", Text: "First message"},
				}},
				{Role: "assistant", Content: []types.ChatContentBlock{
					{Type: "text", Text: "I am the Dean"},
				}},
				{Role: "user", Content: []types.ChatContentBlock{
					{Type: "text", Text: "Troy and Abed in the morning"},
				}},
			},
			want: "Troy and Abed in the morning",
		},
		"empty messages": {
			messages: []types.ChatMessage{},
			want:     "",
		},
		"tool result content": {
			messages: []types.ChatMessage{
				{Role: "user", Content: []types.ChatContentBlock{
					{Type: "text", Text: "Fix the bug at Greendale"},
					{Type: "tool_result", ToolUseID: "toolu_01", Content: []types.ToolResultContent{
						{Type: "text", Text: "Error: Human Being mascot malfunction"},
					}},
				}},
			},
			want: "Fix the bug at Greendale\nError: Human Being mascot malfunction",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got := extractPrompt(types.ChatRequest{Messages: tc.messages})
			r.Equal(tc.want, got)
		})
	}
}

func TestCaptureSessionID(t *testing.T) {
	tests := map[string]struct {
		initial string
		capture []string
		want    string
	}{
		"captures first non-empty": {
			initial: "",
			capture: []string{"", "session-abc", "session-def"},
			want:    "session-abc",
		},
		"does not overwrite": {
			initial: "session-existing",
			capture: []string{"session-new"},
			want:    "session-existing",
		},
		"skips empty": {
			initial: "",
			capture: []string{"", "", ""},
			want:    "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			p := &ClaudeCLIProvider{claudeSessionID: tc.initial}
			for _, id := range tc.capture {
				p.captureSessionID(id)
			}
			r.Equal(tc.want, p.claudeSessionID)
		})
	}
}

func TestHandleStreamEvent(t *testing.T) {
	tests := map[string]struct {
		event json.RawMessage
		want  []types.ChatDelta
	}{
		"text delta": {
			event: mustJSON(cliStreamEvent{
				Type:  "content_block_delta",
				Index: 0,
				Delta: &cliEventDelta{Type: "text_delta", Text: "Cool. Cool cool cool."},
			}),
			want: []types.ChatDelta{
				{Type: "text_delta", Text: "Cool. Cool cool cool."},
			},
		},
		"empty text delta ignored": {
			event: mustJSON(cliStreamEvent{
				Type:  "content_block_delta",
				Index: 0,
				Delta: &cliEventDelta{Type: "text_delta", Text: ""},
			}),
			want: nil,
		},
		"non-text delta ignored": {
			event: mustJSON(cliStreamEvent{
				Type:  "content_block_delta",
				Index: 0,
				Delta: &cliEventDelta{Type: "input_json_delta", PartialJSON: `{"key":"val"}`},
			}),
			want: nil,
		},
		"message_start ignored": {
			event: mustJSON(cliStreamEvent{Type: "message_start"}),
			want:  nil,
		},
		"nil event": {
			event: nil,
			want:  nil,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			p := &ClaudeCLIProvider{}
			ch := make(chan types.ChatDelta, 16)

			p.handleStreamEvent(tc.event, ch)
			close(ch)

			var got []types.ChatDelta
			for d := range ch {
				got = append(got, d)
			}

			r.Equal(tc.want, got)
		})
	}
}

func TestHandleAssistant(t *testing.T) {
	tests := map[string]struct {
		msg  json.RawMessage
		want []types.ChatDelta
	}{
		"with usage": {
			msg: mustJSON(cliAssistantMessage{
				Role: "assistant",
				Content: []cliContentBlock{
					{Type: "text", Text: "Pop pop!"},
				},
				Usage: &cliUsage{
					InputTokens:              100,
					OutputTokens:             42,
					CacheCreationInputTokens: 10,
					CacheReadInputTokens:     50,
				},
			}),
			want: []types.ChatDelta{
				{
					Type: "usage",
					Usage: &types.TokenUsage{
						InputTokens:         100,
						OutputTokens:        42,
						CacheCreationTokens: 10,
						CacheReadTokens:     50,
					},
				},
			},
		},
		"without usage": {
			msg: mustJSON(cliAssistantMessage{
				Role: "assistant",
				Content: []cliContentBlock{
					{Type: "text", Text: "Streets ahead"},
				},
			}),
			want: nil,
		},
		"nil message": {
			msg:  nil,
			want: nil,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			p := &ClaudeCLIProvider{}
			ch := make(chan types.ChatDelta, 16)

			p.handleAssistant(tc.msg, ch)
			close(ch)

			var got []types.ChatDelta
			for d := range ch {
				got = append(got, d)
			}

			r.Equal(tc.want, got)
		})
	}
}

func TestBuildArgs(t *testing.T) {
	tests := map[string]struct {
		req       types.ChatRequest
		prompt    string
		sessionID string
		wantArgs  []string
	}{
		"basic prompt": {
			req:    types.ChatRequest{Model: "sonnet"},
			prompt: "hello",
			wantArgs: []string{
				"-p",
				"--output-format", "stream-json",
				"--include-partial-messages",
				"--dangerously-skip-permissions",
				"--model", "sonnet",
				"hello",
			},
		},
		"with system prompt": {
			req: types.ChatRequest{
				Model: "haiku",
				System: []types.SystemBlock{
					{Text: "You are Troy Barnes"},
					{Text: "From Greendale Community College"},
				},
			},
			prompt: "what's up",
			wantArgs: []string{
				"-p",
				"--output-format", "stream-json",
				"--include-partial-messages",
				"--dangerously-skip-permissions",
				"--model", "haiku",
				"--system-prompt", "You are Troy Barnes\n\nFrom Greendale Community College",
				"what's up",
			},
		},
		"with resume session": {
			req:       types.ChatRequest{Model: "opus"},
			prompt:    "continue",
			sessionID: "session-greendale-123",
			wantArgs: []string{
				"-p",
				"--output-format", "stream-json",
				"--include-partial-messages",
				"--dangerously-skip-permissions",
				"--model", "opus",
				"--resume", "session-greendale-123",
				"continue",
			},
		},
		"no model": {
			req:    types.ChatRequest{},
			prompt: "hey",
			wantArgs: []string{
				"-p",
				"--output-format", "stream-json",
				"--include-partial-messages",
				"--dangerously-skip-permissions",
				"hey",
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			p := &ClaudeCLIProvider{claudeSessionID: tc.sessionID}
			got := p.buildArgs(tc.req, tc.prompt)
			r.Equal(tc.wantArgs, got)
		})
	}
}

// TestChatWithMockScript tests the full Chat flow using a mock "claude" script
// that emits NDJSON to stdout.
func TestChatWithMockScript(t *testing.T) {
	tests := map[string]struct {
		script    string
		wantTypes []string
		wantTexts []string
		wantErr   bool
		wantSID   string
	}{
		"happy path text streaming": {
			script: `#!/bin/sh
echo '{"type":"system","session_id":"sess-troy-barnes","message":"init"}'
echo '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Cool. "}}}'
echo '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Cool cool cool."}}}'
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Cool. Cool cool cool."}],"usage":{"input_tokens":10,"output_tokens":5}},"session_id":"sess-troy-barnes"}'
echo '{"type":"result","session_id":"sess-troy-barnes","is_error":false,"duration_ms":100}'
`,
			wantTypes: []string{"text_delta", "text_delta", "usage", "message_stop"},
			wantTexts: []string{"Cool. ", "Cool cool cool.", "", ""},
			wantSID:   "sess-troy-barnes",
		},
		"error result": {
			script: `#!/bin/sh
echo '{"type":"system","session_id":"sess-chang","message":"init"}'
echo '{"type":"result","session_id":"sess-chang","is_error":true,"duration_ms":50}'
`,
			wantTypes: []string{"error"},
			wantTexts: []string{"Claude CLI returned an error"},
			wantSID:   "sess-chang",
		},
		"empty output": {
			script: `#!/bin/sh
# Just exit
`,
			wantTypes: []string{"message_stop"},
			wantTexts: []string{""},
			wantSID:   "",
		},
		"stderr error": {
			script: `#!/bin/sh
echo "Error: Señor Chang denied access" >&2
`,
			wantTypes: []string{"error"},
			wantTexts: []string{"claude CLI error: Error: Señor Chang denied access"},
			wantSID:   "",
		},
		"tool use in stream ignored": {
			script: `#!/bin/sh
echo '{"type":"system","session_id":"sess-abed","message":"init"}'
echo '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Let me check..."}}}'
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01","name":"Read","input":{"file_path":"/tmp/test"}}]},"session_id":"sess-abed"}'
echo '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Found it!"}}}'
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Found it!"}],"usage":{"input_tokens":20,"output_tokens":10}},"session_id":"sess-abed"}'
echo '{"type":"result","session_id":"sess-abed","is_error":false,"duration_ms":200}'
`,
			wantTypes: []string{"text_delta", "text_delta", "usage", "message_stop"},
			wantTexts: []string{"Let me check...", "Found it!", "", ""},
			wantSID:   "sess-abed",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			// Create a mock "claude" script.
			tmpDir := t.TempDir()
			mockBin := filepath.Join(tmpDir, "claude")
			err := os.WriteFile(mockBin, []byte(tc.script), 0o755)
			r.NoError(err)

			// Override PATH to use our mock.
			t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

			p := NewClaudeCLI()
			req := types.ChatRequest{
				Model: "haiku",
				Messages: []types.ChatMessage{
					{Role: "user", Content: []types.ChatContentBlock{
						{Type: "text", Text: "test prompt"},
					}},
				},
			}

			ch, err := p.Chat(context.Background(), req)
			r.NoError(err)

			var gotTypes, gotTexts []string
			for delta := range ch {
				gotTypes = append(gotTypes, delta.Type)
				gotTexts = append(gotTexts, delta.Text)
			}

			r.Equal(tc.wantTypes, gotTypes)
			r.Equal(tc.wantTexts, gotTexts)
			r.Equal(tc.wantSID, p.claudeSessionID)
		})
	}
}

// TestChatContextCancellation verifies the process is killed on context cancel.
func TestChatContextCancellation(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "claude")
	// Script that sleeps forever — should be killed by context cancel.
	err := os.WriteFile(mockBin, []byte(`#!/bin/sh
echo '{"type":"system","session_id":"sess-cancel","message":"init"}'
sleep 60
`), 0o755)
	r.NoError(err)
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	p := NewClaudeCLI()
	ctx, cancel := context.WithCancel(context.Background())

	ch, err := p.Chat(ctx, types.ChatRequest{
		Model: "haiku",
		Messages: []types.ChatMessage{
			{Role: "user", Content: []types.ChatContentBlock{
				{Type: "text", Text: "testing cancel"},
			}},
		},
	})
	r.NoError(err)

	// Cancel after we've started.
	cancel()

	// Channel should close without hanging.
	for range ch {
	}
	// We don't assert specific deltas or session ID — the cancel may fire
	// before any output is read. The test validates we don't hang.
}

// TestChatSessionReuse verifies --resume is passed on second call.
func TestChatSessionReuse(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "args.log")

	// Mock that logs its args and emits minimal NDJSON.
	mockBin := filepath.Join(tmpDir, "claude")
	script := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %s
echo '{"type":"system","session_id":"sess-study-group","message":"init"}'
echo '{"type":"result","session_id":"sess-study-group","is_error":false}'
`, argsFile)
	err := os.WriteFile(mockBin, []byte(script), 0o755)
	r.NoError(err)
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	p := NewClaudeCLI()
	req := types.ChatRequest{
		Model: "haiku",
		Messages: []types.ChatMessage{
			{Role: "user", Content: []types.ChatContentBlock{
				{Type: "text", Text: "first message"},
			}},
		},
	}

	// First call — no --resume.
	ch, err := p.Chat(context.Background(), req)
	r.NoError(err)
	for range ch {
	}

	r.Equal("sess-study-group", p.claudeSessionID)

	// Second call — should include --resume.
	req.Messages = []types.ChatMessage{
		{Role: "user", Content: []types.ChatContentBlock{
			{Type: "text", Text: "second message"},
		}},
	}
	ch, err = p.Chat(context.Background(), req)
	r.NoError(err)
	for range ch {
	}

	// Check the logged args.
	argsBytes, err := os.ReadFile(argsFile)
	r.NoError(err)
	lines := strings.Split(strings.TrimSpace(string(argsBytes)), "\n")
	r.Len(lines, 2)
	r.NotContains(lines[0], "--resume")
	r.Contains(lines[1], "--resume sess-study-group")
}

// TestChatClaudeNotOnPath verifies a clear error when claude is missing.
func TestChatClaudeNotOnPath(t *testing.T) {
	r := require.New(t)
	t.Setenv("PATH", t.TempDir()) // empty PATH

	p := NewClaudeCLI()
	_, err := p.Chat(context.Background(), types.ChatRequest{
		Messages: []types.ChatMessage{
			{Role: "user", Content: []types.ChatContentBlock{
				{Type: "text", Text: "hello"},
			}},
		},
	})
	r.Error(err)
	r.Contains(err.Error(), "claude CLI not found")
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
