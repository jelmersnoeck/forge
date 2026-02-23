package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"time"
)

// OpenCode shells out to the `opencode` CLI for LLM execution.
type OpenCode struct {
	// Path is the path to the opencode binary. Defaults to "opencode".
	Path string
}

// NewOpenCode creates a new OpenCode agent with the given binary path.
// If path is empty, it defaults to "opencode".
func NewOpenCode(path string) *OpenCode {
	if path == "" {
		path = "opencode"
	}
	return &OpenCode{Path: path}
}

// openCodeOutput is the JSON structure returned by `opencode -f json`.
type openCodeOutput struct {
	Result   string  `json:"result"`
	Duration float64 `json:"duration"`
	Error    string  `json:"error,omitempty"`
}

// Run executes a prompt via the opencode CLI.
func (o *OpenCode) Run(ctx context.Context, req Request) (*Response, error) {
	args := o.buildArgs(req)

	slog.InfoContext(ctx, "running opencode agent",
		"workdir", req.WorkDir,
		"mode", req.Mode,
		"args_count", len(args),
	)

	start := time.Now()

	cmd := exec.CommandContext(ctx, o.Path, args...)
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := time.Since(start).Seconds()

	if ctx.Err() != nil {
		return nil, fmt.Errorf("opencode agent: %w", ctx.Err())
	}

	if err != nil {
		return &Response{
			Output:   stdout.String(),
			ExitCode: cmd.ProcessState.ExitCode(),
			Duration: duration,
			Error:    fmt.Sprintf("opencode agent: %s: %s", err.Error(), stderr.String()),
		}, fmt.Errorf("opencode agent: %w: %s", err, stderr.String())
	}

	resp, parseErr := o.parseOutput(stdout.Bytes(), duration)
	if parseErr != nil {
		// Fall back to raw output if JSON parsing fails.
		return &Response{
			Output:   stdout.String(),
			ExitCode: 0,
			Duration: duration,
		}, nil
	}

	return resp, nil
}

// buildArgs constructs the CLI arguments for the opencode command.
func (o *OpenCode) buildArgs(req Request) []string {
	args := []string{"-p", req.Prompt, "-f", "json"}

	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}

	return args
}

// parseOutput parses the JSON output from the opencode CLI.
func (o *OpenCode) parseOutput(data []byte, duration float64) (*Response, error) {
	var out openCodeOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse opencode output: %w", err)
	}

	return &Response{
		Output:   out.Result,
		ExitCode: 0,
		Duration: duration,
		Error:    out.Error,
	}, nil
}
