package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// ClaudeCode shells out to the `claude` CLI for LLM execution.
type ClaudeCode struct {
	// Path is the path to the claude binary. Defaults to "claude".
	Path string
}

// NewClaudeCode creates a new ClaudeCode agent with the given binary path.
// If path is empty, it defaults to "claude".
func NewClaudeCode(path string) *ClaudeCode {
	if path == "" {
		path = "claude"
	}
	return &ClaudeCode{Path: path}
}

// claudeOutput is the JSON structure returned by `claude --output-format json`.
type claudeOutput struct {
	Result   string `json:"result"`
	Duration float64 `json:"duration"`
	Cost     *struct {
		InputTokens  int     `json:"input_tokens"`
		OutputTokens int     `json:"output_tokens"`
		TotalCost    float64 `json:"total_cost"`
	} `json:"cost,omitempty"`
	Error string `json:"error,omitempty"`
}

// Run executes a prompt via the claude CLI.
func (c *ClaudeCode) Run(ctx context.Context, req Request) (*Response, error) {
	args := c.buildArgs(req)

	slog.InfoContext(ctx, "running claude agent",
		"workdir", req.WorkDir,
		"mode", req.Mode,
		"args_count", len(args),
	)

	start := time.Now()

	cmd := exec.CommandContext(ctx, c.Path, args...)
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := time.Since(start).Seconds()

	if ctx.Err() != nil {
		return nil, fmt.Errorf("claude agent: %w", ctx.Err())
	}

	if err != nil {
		return &Response{
			Output:   stdout.String(),
			ExitCode: cmd.ProcessState.ExitCode(),
			Duration: duration,
			Error:    fmt.Sprintf("claude agent: %s: %s", err.Error(), stderr.String()),
		}, fmt.Errorf("claude agent: %w: %s", err, stderr.String())
	}

	resp, parseErr := c.parseOutput(stdout.Bytes(), duration)
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

// buildArgs constructs the CLI arguments for the claude command.
func (c *ClaudeCode) buildArgs(req Request) []string {
	args := []string{"-p", req.Prompt, "--output-format", "json"}

	if tools := modeToClaudeTools(req.Mode, req.Permissions); len(tools) > 0 {
		args = append(args, "--allowedTools", strings.Join(tools, ","))
	}

	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}

	return args
}

// modeToClaudeTools maps a Mode and ToolPermissions to claude --allowedTools values.
func modeToClaudeTools(mode Mode, perms ToolPermissions) []string {
	switch mode {
	case ModePlan:
		return []string{"View", "Read"}
	case ModeReview:
		return []string{"View", "Read", "Grep"}
	case ModeCode:
		var tools []string
		if perms.Read {
			tools = append(tools, "View", "Read", "Grep")
		}
		if perms.Write {
			tools = append(tools, "Edit", "Write", "MultiEdit")
		}
		if perms.Execute {
			tools = append(tools, "Bash")
		}
		if perms.Network {
			tools = append(tools, "WebFetch")
		}
		if len(tools) == 0 {
			// Default code tools when no permissions are specified.
			return []string{"View", "Read", "Grep", "Edit", "Write", "Bash"}
		}
		return tools
	default:
		return nil
	}
}

// parseOutput parses the JSON output from the claude CLI.
func (c *ClaudeCode) parseOutput(data []byte, duration float64) (*Response, error) {
	var out claudeOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse claude output: %w", err)
	}

	resp := &Response{
		Output:   out.Result,
		ExitCode: 0,
		Duration: duration,
		Error:    out.Error,
	}

	if out.Cost != nil {
		resp.Cost = &Cost{
			InputTokens:  out.Cost.InputTokens,
			OutputTokens: out.Cost.OutputTokens,
			TotalCost:    out.Cost.TotalCost,
		}
	}

	return resp, nil
}
