package provider

import (
	"context"
	"encoding/json"
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
	r.False(p.alive)
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

func TestBuildOneShotArgs(t *testing.T) {
	tests := map[string]struct {
		req      types.ChatRequest
		prompt   string
		wantArgs []string
	}{
		"basic prompt": {
			req:    types.ChatRequest{Model: "sonnet"},
			prompt: "hello",
			wantArgs: []string{
				"-p",
				"--verbose",
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
				"--verbose",
				"--output-format", "stream-json",
				"--include-partial-messages",
				"--dangerously-skip-permissions",
				"--model", "haiku",
				"--system-prompt", "You are Troy Barnes\n\nFrom Greendale Community College",
				"what's up",
			},
		},
		"no model": {
			req:    types.ChatRequest{},
			prompt: "hey",
			wantArgs: []string{
				"-p",
				"--verbose",
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
			got := buildOneShotArgs(tc.req, tc.prompt)
			r.Equal(tc.wantArgs, got)
		})
	}
}

func TestBuildPromptWithSystem(t *testing.T) {
	tests := map[string]struct {
		req  types.ChatRequest
		want string
	}{
		"no system prompt": {
			req: types.ChatRequest{
				Messages: []types.ChatMessage{
					{Role: "user", Content: []types.ChatContentBlock{
						{Type: "text", Text: "hello"},
					}},
				},
			},
			want: "hello",
		},
		"with system prompt": {
			req: types.ChatRequest{
				System: []types.SystemBlock{
					{Text: "You are Troy Barnes"},
					{Text: "From Greendale"},
				},
				Messages: []types.ChatMessage{
					{Role: "user", Content: []types.ChatContentBlock{
						{Type: "text", Text: "what's up"},
					}},
				},
			},
			want: "[System Instructions]\nYou are Troy Barnes\n\nFrom Greendale\n\n[User Message]\nwhat's up",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got := buildPromptWithSystem(tc.req)
			r.Equal(tc.want, got)
		})
	}
}

// persistentMockScript returns a shell script that reads NDJSON from stdin
// and emits a response for each message, staying alive between turns.
func persistentMockScript() string {
	return `#!/bin/sh
while IFS= read -r line; do
    echo '{"type":"system","session_id":"sess-persistent","message":"init"}'
    echo '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Cool. Cool cool cool."}}}'
    echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Cool. Cool cool cool."}],"usage":{"input_tokens":10,"output_tokens":5}},"session_id":"sess-persistent"}'
    echo '{"type":"result","session_id":"sess-persistent","is_error":false,"duration_ms":100}'
done
`
}

// TestPersistentProcessChat verifies the basic persistent process flow.
func TestPersistentProcessChat(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "claude")
	err := os.WriteFile(mockBin, []byte(persistentMockScript()), 0o755)
	r.NoError(err)
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	p := NewClaudeCLI()
	defer p.Close()

	req := types.ChatRequest{
		Model: "opus",
		Messages: []types.ChatMessage{
			{Role: "user", Content: []types.ChatContentBlock{
				{Type: "text", Text: "test prompt"},
			}},
		},
	}

	ch, err := p.Chat(context.Background(), req)
	r.NoError(err)

	var gotTypes []string
	for delta := range ch {
		gotTypes = append(gotTypes, delta.Type)
	}

	r.Equal([]string{"text_delta", "usage", "message_stop"}, gotTypes)
	r.Equal("sess-persistent", p.claudeSessionID)
	r.True(p.alive, "process should stay alive after first call")
}

// TestPersistentProcessReuse verifies two sequential calls reuse the same process.
func TestPersistentProcessReuse(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "claude")
	err := os.WriteFile(mockBin, []byte(persistentMockScript()), 0o755)
	r.NoError(err)
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	p := NewClaudeCLI()
	defer p.Close()

	req := types.ChatRequest{
		Model: "opus",
		Messages: []types.ChatMessage{
			{Role: "user", Content: []types.ChatContentBlock{
				{Type: "text", Text: "first"},
			}},
		},
	}

	// First call — spawns the process.
	ch, err := p.Chat(context.Background(), req)
	r.NoError(err)
	for range ch {
	}

	// Capture the process pointer.
	firstCmd := p.cmd

	// Second call — should reuse.
	req.Messages = []types.ChatMessage{
		{Role: "user", Content: []types.ChatContentBlock{
			{Type: "text", Text: "second"},
		}},
	}
	ch, err = p.Chat(context.Background(), req)
	r.NoError(err)
	for range ch {
	}

	r.Same(firstCmd, p.cmd, "should reuse the same process")
	r.True(p.alive)
}

// TestPersistentProcessRespawn verifies respawn after process death.
func TestPersistentProcessRespawn(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "claude")
	err := os.WriteFile(mockBin, []byte(persistentMockScript()), 0o755)
	r.NoError(err)
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	p := NewClaudeCLI()
	defer p.Close()

	req := types.ChatRequest{
		Model: "opus",
		Messages: []types.ChatMessage{
			{Role: "user", Content: []types.ChatContentBlock{
				{Type: "text", Text: "first"},
			}},
		},
	}

	// First call.
	ch, err := p.Chat(context.Background(), req)
	r.NoError(err)
	for range ch {
	}
	r.True(p.alive)

	// Kill the process externally.
	p.mu.Lock()
	_ = p.killProcess()
	p.mu.Unlock()
	r.False(p.alive)

	// Next call should respawn.
	ch, err = p.Chat(context.Background(), req)
	r.NoError(err)
	for range ch {
	}
	r.True(p.alive, "should have respawned")
}

// TestModelMismatchUsesOneShot verifies that a different model triggers one-shot mode.
func TestModelMismatchUsesOneShot(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "claude")

	// Script that handles both persistent (stdin) and one-shot (arg) modes.
	// One-shot: prompt is in $@ args. Persistent: prompt comes from stdin.
	script := `#!/bin/sh
# If we have args with the prompt, we're in one-shot mode
if echo "$@" | grep -q "classify this"; then
    echo '{"type":"system","session_id":"sess-oneshot","message":"init"}'
    echo '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"oneshot"}}}'
    echo '{"type":"result","session_id":"sess-oneshot","is_error":false}'
    exit 0
fi

# Otherwise persistent mode
while IFS= read -r line; do
    echo '{"type":"system","session_id":"sess-persistent","message":"init"}'
    echo '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"persistent"}}}'
    echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"persistent"}],"usage":{"input_tokens":10,"output_tokens":5}},"session_id":"sess-persistent"}'
    echo '{"type":"result","session_id":"sess-persistent","is_error":false}'
done
`
	err := os.WriteFile(mockBin, []byte(script), 0o755)
	r.NoError(err)
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	p := NewClaudeCLI()
	defer p.Close()

	// First call with opus — starts persistent process.
	ch, err := p.Chat(context.Background(), types.ChatRequest{
		Model: "opus",
		Messages: []types.ChatMessage{
			{Role: "user", Content: []types.ChatContentBlock{
				{Type: "text", Text: "main task"},
			}},
		},
	})
	r.NoError(err)

	var firstTexts []string
	for delta := range ch {
		if delta.Type == "text_delta" {
			firstTexts = append(firstTexts, delta.Text)
		}
	}
	r.Equal([]string{"persistent"}, firstTexts)
	r.Equal("opus", p.model)
	r.True(p.alive)

	// Second call with haiku — should use one-shot.
	ch, err = p.Chat(context.Background(), types.ChatRequest{
		Model: "haiku",
		Messages: []types.ChatMessage{
			{Role: "user", Content: []types.ChatContentBlock{
				{Type: "text", Text: "classify this"},
			}},
		},
	})
	r.NoError(err)

	var secondTexts []string
	for delta := range ch {
		if delta.Type == "text_delta" {
			secondTexts = append(secondTexts, delta.Text)
		}
	}
	r.Equal([]string{"oneshot"}, secondTexts)

	// Persistent process should still be alive.
	r.True(p.alive)
}

// TestClose verifies Close() kills the process.
func TestClose(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "claude")
	err := os.WriteFile(mockBin, []byte(persistentMockScript()), 0o755)
	r.NoError(err)
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	p := NewClaudeCLI()

	// Start a session.
	ch, err := p.Chat(context.Background(), types.ChatRequest{
		Model: "opus",
		Messages: []types.ChatMessage{
			{Role: "user", Content: []types.ChatContentBlock{
				{Type: "text", Text: "hello"},
			}},
		},
	})
	r.NoError(err)
	for range ch {
	}
	r.True(p.alive)

	// Close.
	err = p.Close()
	r.NoError(err)
	r.False(p.alive)
	r.Nil(p.cmd)

	// Double close should be safe.
	err = p.Close()
	r.NoError(err)
}

// TestSystemPromptInjection verifies system prompt is prepended to user message.
func TestSystemPromptInjection(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	inputLog := filepath.Join(tmpDir, "input.log")
	mockBin := filepath.Join(tmpDir, "claude")

	// Script that logs stdin input and emits a response.
	// Use printf to avoid shell interpretation of escape sequences in JSON.
	script := `#!/bin/sh
while IFS= read -r line; do
    printf '%s\n' "$line" >> ` + inputLog + `
    echo '{"type":"system","session_id":"sess-sys","message":"init"}'
    echo '{"type":"result","session_id":"sess-sys","is_error":false}'
done
`
	err := os.WriteFile(mockBin, []byte(script), 0o755)
	r.NoError(err)
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	p := NewClaudeCLI()
	defer p.Close()

	ch, err := p.Chat(context.Background(), types.ChatRequest{
		Model: "opus",
		System: []types.SystemBlock{
			{Text: "You are the Dean of Greendale"},
		},
		Messages: []types.ChatMessage{
			{Role: "user", Content: []types.ChatContentBlock{
				{Type: "text", Text: "introduce yourself"},
			}},
		},
	})
	r.NoError(err)
	for range ch {
	}

	// Read what was sent to stdin.
	inputBytes, err := os.ReadFile(inputLog)
	r.NoError(err)

	var msg cliInputMessage
	err = json.Unmarshal(inputBytes, &msg)
	r.NoError(err)

	r.Equal("user", msg.Type)
	r.Equal("user", msg.Message.Role)
	r.Contains(msg.Message.Content, "[System Instructions]")
	r.Contains(msg.Message.Content, "You are the Dean of Greendale")
	r.Contains(msg.Message.Content, "[User Message]")
	r.Contains(msg.Message.Content, "introduce yourself")
}

// TestOneShotChat verifies the one-shot fallback works for non-persistent calls.
func TestOneShotChat(t *testing.T) {
	tests := map[string]struct {
		script    string
		wantTypes []string
		wantTexts []string
		wantErr   bool
	}{
		"happy path": {
			script: `#!/bin/sh
echo '{"type":"system","session_id":"sess-troy","message":"init"}'
echo '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Cool. Cool cool cool."}}}'
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Cool. Cool cool cool."}],"usage":{"input_tokens":10,"output_tokens":5}},"session_id":"sess-troy"}'
echo '{"type":"result","session_id":"sess-troy","is_error":false,"duration_ms":100}'
`,
			wantTypes: []string{"text_delta", "usage", "message_stop"},
			wantTexts: []string{"Cool. Cool cool cool.", "", ""},
		},
		"error result": {
			script: `#!/bin/sh
echo '{"type":"system","session_id":"sess-chang","message":"init"}'
echo '{"type":"result","session_id":"sess-chang","is_error":true}'
`,
			wantTypes: []string{"error"},
			wantTexts: []string{"Claude CLI returned an error"},
		},
		"stderr error": {
			script: `#!/bin/sh
echo "Error: Señor Chang denied access" >&2
`,
			wantTypes: []string{"error"},
			wantTexts: []string{"claude CLI error: Error: Señor Chang denied access"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			tmpDir := t.TempDir()
			mockBin := filepath.Join(tmpDir, "claude")
			err := os.WriteFile(mockBin, []byte(tc.script), 0o755)
			r.NoError(err)
			t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

			p := NewClaudeCLI()
			// Force one-shot by making the persistent process have a
			// different model locked in.
			p.alive = true
			p.model = "opus"

			ch, err := p.Chat(context.Background(), types.ChatRequest{
				Model: "haiku",
				Messages: []types.ChatMessage{
					{Role: "user", Content: []types.ChatContentBlock{
						{Type: "text", Text: "test"},
					}},
				},
			})
			r.NoError(err)

			var gotTypes, gotTexts []string
			for delta := range ch {
				gotTypes = append(gotTypes, delta.Type)
				gotTexts = append(gotTexts, delta.Text)
			}

			r.Equal(tc.wantTypes, gotTypes)
			r.Equal(tc.wantTexts, gotTexts)
		})
	}
}

// TestChatContextCancellation verifies the process is killed on cancel and respawns.
func TestChatContextCancellation(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "claude")
	// Script that reads stdin, emits a text delta, then sleeps (simulating
	// a long response). On cancellation the process should be killed.
	err := os.WriteFile(mockBin, []byte(`#!/bin/sh
while IFS= read -r line; do
    echo '{"type":"system","session_id":"sess-cancel","message":"init"}'
    echo '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"working..."}}}'
    sleep 60
    echo '{"type":"result","session_id":"sess-cancel","is_error":false}'
done
`), 0o755)
	r.NoError(err)
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	p := NewClaudeCLI()
	defer p.Close()

	ctx, cancel := context.WithCancel(context.Background())

	ch, err := p.Chat(ctx, types.ChatRequest{
		Model: "opus",
		Messages: []types.ChatMessage{
			{Role: "user", Content: []types.ChatContentBlock{
				{Type: "text", Text: "testing cancel"},
			}},
		},
	})
	r.NoError(err)

	// Read at least one delta to confirm the process started.
	delta := <-ch
	r.Equal("text_delta", delta.Type)

	// Cancel the context — should kill the process.
	cancel()

	// Channel should close without hanging.
	for range ch {
	}

	r.False(p.alive, "process should be killed on cancellation")

	// Rewrite mock to a non-sleeping version for the respawn test.
	err = os.WriteFile(mockBin, []byte(persistentMockScript()), 0o755)
	r.NoError(err)

	// Next call should respawn the process.
	ch, err = p.Chat(context.Background(), types.ChatRequest{
		Model: "opus",
		Messages: []types.ChatMessage{
			{Role: "user", Content: []types.ChatContentBlock{
				{Type: "text", Text: "after cancel"},
			}},
		},
	})
	r.NoError(err)

	var gotTypes []string
	for delta := range ch {
		gotTypes = append(gotTypes, delta.Type)
	}
	r.Contains(gotTypes, "text_delta", "should have respawned and produced output")
	r.True(p.alive, "should be alive after respawn")
}

// TestChatClaudeNotOnPath verifies a clear error when claude is missing.
func TestChatClaudeNotOnPath(t *testing.T) {
	r := require.New(t)
	t.Setenv("PATH", t.TempDir())

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

// TestProcessDiesEmitsError verifies error handling when the process exits unexpectedly.
func TestProcessDiesEmitsError(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "claude")
	// Script that reads one message, emits system init, then exits without result.
	err := os.WriteFile(mockBin, []byte(`#!/bin/sh
IFS= read -r line
echo '{"type":"system","session_id":"sess-die","message":"init"}'
# Exit without sending result — simulates crash.
`), 0o755)
	r.NoError(err)
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	p := NewClaudeCLI()

	ch, err := p.Chat(context.Background(), types.ChatRequest{
		Model: "opus",
		Messages: []types.ChatMessage{
			{Role: "user", Content: []types.ChatContentBlock{
				{Type: "text", Text: "hello"},
			}},
		},
	})
	r.NoError(err)

	var gotTypes []string
	for delta := range ch {
		gotTypes = append(gotTypes, delta.Type)
		if delta.Type == "error" {
			r.Contains(strings.ToLower(delta.Text), "terminated unexpectedly")
		}
	}

	r.Contains(gotTypes, "error")
	r.False(p.alive, "process should be marked as dead")
}
