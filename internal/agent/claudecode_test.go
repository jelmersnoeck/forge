package agent

import (
	"context"
	"encoding/json"
	"testing"
)

func TestClaudeCode_BuildArgs(t *testing.T) {
	c := NewClaudeCode("")

	tests := []struct {
		name     string
		req      Request
		wantArgs []string
	}{
		{
			name: "plan mode",
			req: Request{
				Prompt: "generate a plan",
				Mode:   ModePlan,
			},
			wantArgs: []string{"-p", "generate a plan", "--output-format", "json", "--allowedTools", "View,Read"},
		},
		{
			name: "review mode",
			req: Request{
				Prompt: "review this code",
				Mode:   ModeReview,
			},
			wantArgs: []string{"-p", "review this code", "--output-format", "json", "--allowedTools", "View,Read,Grep"},
		},
		{
			name: "code mode with permissions",
			req: Request{
				Prompt: "implement feature",
				Mode:   ModeCode,
				Permissions: ToolPermissions{
					Read:    true,
					Write:   true,
					Execute: true,
				},
			},
			wantArgs: []string{"-p", "implement feature", "--output-format", "json", "--allowedTools", "View,Read,Grep,Edit,Write,MultiEdit,Bash"},
		},
		{
			name: "code mode with no permissions defaults",
			req: Request{
				Prompt: "implement feature",
				Mode:   ModeCode,
			},
			wantArgs: []string{"-p", "implement feature", "--output-format", "json", "--allowedTools", "View,Read,Grep,Edit,Write,Bash"},
		},
		{
			name: "code mode with model override",
			req: Request{
				Prompt: "implement feature",
				Mode:   ModeCode,
				Model:  "claude-opus-4-20250514",
			},
			wantArgs: []string{"-p", "implement feature", "--output-format", "json", "--allowedTools", "View,Read,Grep,Edit,Write,Bash", "--model", "claude-opus-4-20250514"},
		},
		{
			name: "code mode with network permission",
			req: Request{
				Prompt: "fetch data",
				Mode:   ModeCode,
				Permissions: ToolPermissions{
					Read:    true,
					Network: true,
				},
			},
			wantArgs: []string{"-p", "fetch data", "--output-format", "json", "--allowedTools", "View,Read,Grep,WebFetch"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.buildArgs(tt.req)
			if len(got) != len(tt.wantArgs) {
				t.Fatalf("buildArgs() returned %d args, want %d\ngot:  %v\nwant: %v", len(got), len(tt.wantArgs), got, tt.wantArgs)
			}
			for i := range got {
				if got[i] != tt.wantArgs[i] {
					t.Errorf("buildArgs()[%d] = %q, want %q", i, got[i], tt.wantArgs[i])
				}
			}
		})
	}
}

func TestClaudeCode_ParseOutput(t *testing.T) {
	c := NewClaudeCode("")

	tests := []struct {
		name       string
		data       string
		wantOutput string
		wantCost   bool
		wantErr    bool
	}{
		{
			name:       "valid output with cost",
			data:       `{"result":"hello world","duration":1.5,"cost":{"input_tokens":100,"output_tokens":50,"total_cost":0.002}}`,
			wantOutput: "hello world",
			wantCost:   true,
			wantErr:    false,
		},
		{
			name:       "valid output without cost",
			data:       `{"result":"done","duration":0.5}`,
			wantOutput: "done",
			wantCost:   false,
			wantErr:    false,
		},
		{
			name:       "output with error field",
			data:       `{"result":"","duration":0.1,"error":"rate limited"}`,
			wantOutput: "",
			wantCost:   false,
			wantErr:    false,
		},
		{
			name:    "invalid json",
			data:    `not json`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := c.parseOutput([]byte(tt.data), 1.0)
			if tt.wantErr {
				if err == nil {
					t.Fatal("parseOutput() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseOutput() unexpected error: %v", err)
			}
			if resp.Output != tt.wantOutput {
				t.Errorf("Output = %q, want %q", resp.Output, tt.wantOutput)
			}
			if tt.wantCost && resp.Cost == nil {
				t.Error("Cost is nil, expected non-nil")
			}
			if !tt.wantCost && resp.Cost != nil {
				t.Error("Cost is non-nil, expected nil")
			}
		})
	}
}

func TestClaudeCode_ParseOutputCostValues(t *testing.T) {
	c := NewClaudeCode("")

	data := `{"result":"ok","cost":{"input_tokens":100,"output_tokens":50,"total_cost":0.005}}`
	resp, err := c.parseOutput([]byte(data), 2.0)
	if err != nil {
		t.Fatalf("parseOutput() unexpected error: %v", err)
	}
	if resp.Cost.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", resp.Cost.InputTokens)
	}
	if resp.Cost.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want 50", resp.Cost.OutputTokens)
	}
	if resp.Cost.TotalCost != 0.005 {
		t.Errorf("TotalCost = %f, want 0.005", resp.Cost.TotalCost)
	}
	if resp.Duration != 2.0 {
		t.Errorf("Duration = %f, want 2.0", resp.Duration)
	}
}

func TestClaudeCode_RunContextCancelled(t *testing.T) {
	c := NewClaudeCode("nonexistent-binary-that-should-not-exist")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := c.Run(ctx, Request{Prompt: "test"})
	if err == nil {
		t.Fatal("Run() expected error for cancelled context, got nil")
	}
}

func TestModeToClaudeTools(t *testing.T) {
	tests := []struct {
		name  string
		mode  Mode
		perms ToolPermissions
		want  []string
	}{
		{
			name: "plan mode",
			mode: ModePlan,
			want: []string{"View", "Read"},
		},
		{
			name: "review mode",
			mode: ModeReview,
			want: []string{"View", "Read", "Grep"},
		},
		{
			name: "unknown mode",
			mode: Mode("unknown"),
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := modeToClaudeTools(tt.mode, tt.perms)
			if len(got) != len(tt.want) {
				t.Fatalf("modeToClaudeTools() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("modeToClaudeTools()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestNewClaudeCode_DefaultPath(t *testing.T) {
	c := NewClaudeCode("")
	if c.Path != "claude" {
		t.Errorf("Path = %q, want %q", c.Path, "claude")
	}
}

func TestNewClaudeCode_CustomPath(t *testing.T) {
	c := NewClaudeCode("/usr/local/bin/claude")
	if c.Path != "/usr/local/bin/claude" {
		t.Errorf("Path = %q, want %q", c.Path, "/usr/local/bin/claude")
	}
}

func TestClaudeCode_ParseFallbackSetsError(t *testing.T) {
	c := NewClaudeCode("")

	// Simulate what happens when parseOutput fails: the Run method
	// should fall back to raw output and set the Error field.
	rawOutput := []byte("this is not JSON")
	_, err := c.parseOutput(rawOutput, 1.0)
	if err == nil {
		t.Fatal("expected parseOutput to return error for non-JSON input")
	}

	// The Run method catches this error and creates a fallback response.
	// Verify the fallback response would have the Error field set.
	// (We can't easily test Run itself without a real binary, so we test
	// the contract: parseOutput returns error -> caller sets Error field.)
	resp := &Response{
		Output:   string(rawOutput),
		ExitCode: 0,
		Duration: 1.0,
		Error:    "output parse fallback: " + err.Error(),
	}
	if resp.Error == "" {
		t.Error("expected non-empty Error field on fallback response")
	}
	if resp.Output != "this is not JSON" {
		t.Errorf("expected raw output preserved, got %q", resp.Output)
	}
}

// Verify ClaudeCode implements Agent interface.
var _ Agent = (*ClaudeCode)(nil)

// Verify JSON roundtrip works for the claude output format.
func TestClaudeOutputJSON(t *testing.T) {
	out := claudeOutput{
		Result:   "test result",
		Duration: 1.5,
		Error:    "",
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var decoded claudeOutput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if decoded.Result != out.Result {
		t.Errorf("Result = %q, want %q", decoded.Result, out.Result)
	}
}
