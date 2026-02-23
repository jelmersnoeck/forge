package agent

import (
	"context"
	"encoding/json"
	"testing"
)

func TestOpenCode_BuildArgs(t *testing.T) {
	o := NewOpenCode("")

	tests := []struct {
		name     string
		req      Request
		wantArgs []string
	}{
		{
			name: "basic prompt",
			req: Request{
				Prompt: "generate a plan",
			},
			wantArgs: []string{"-p", "generate a plan", "-f", "json"},
		},
		{
			name: "with model override",
			req: Request{
				Prompt: "implement feature",
				Model:  "gpt-4o",
			},
			wantArgs: []string{"-p", "implement feature", "-f", "json", "--model", "gpt-4o"},
		},
		{
			name: "empty model not added",
			req: Request{
				Prompt: "review code",
				Mode:   ModeReview,
			},
			wantArgs: []string{"-p", "review code", "-f", "json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := o.buildArgs(tt.req)
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

func TestOpenCode_ParseOutput(t *testing.T) {
	o := NewOpenCode("")

	tests := []struct {
		name       string
		data       string
		wantOutput string
		wantErr    bool
	}{
		{
			name:       "valid output",
			data:       `{"result":"hello world","duration":1.5}`,
			wantOutput: "hello world",
			wantErr:    false,
		},
		{
			name:       "output with error",
			data:       `{"result":"","duration":0.1,"error":"model error"}`,
			wantOutput: "",
			wantErr:    false,
		},
		{
			name:    "invalid json",
			data:    `{broken`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := o.parseOutput([]byte(tt.data), 1.0)
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
		})
	}
}

func TestOpenCode_RunContextCancelled(t *testing.T) {
	o := NewOpenCode("nonexistent-binary-that-should-not-exist")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := o.Run(ctx, Request{Prompt: "test"})
	if err == nil {
		t.Fatal("Run() expected error for cancelled context, got nil")
	}
}

func TestNewOpenCode_DefaultPath(t *testing.T) {
	o := NewOpenCode("")
	if o.Path != "opencode" {
		t.Errorf("Path = %q, want %q", o.Path, "opencode")
	}
}

func TestNewOpenCode_CustomPath(t *testing.T) {
	o := NewOpenCode("/usr/local/bin/opencode")
	if o.Path != "/usr/local/bin/opencode" {
		t.Errorf("Path = %q, want %q", o.Path, "/usr/local/bin/opencode")
	}
}

// Verify OpenCode implements Agent interface.
var _ Agent = (*OpenCode)(nil)

// Verify JSON roundtrip works for the opencode output format.
func TestOpenCodeOutputJSON(t *testing.T) {
	out := openCodeOutput{
		Result:   "test result",
		Duration: 2.0,
		Error:    "",
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var decoded openCodeOutput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if decoded.Result != out.Result {
		t.Errorf("Result = %q, want %q", decoded.Result, out.Result)
	}
}
