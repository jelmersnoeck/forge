package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
)

// alwaysInteractive are commands that need a TTY regardless of arguments.
var alwaysInteractive = map[string]string{
	"vim":    "Use 'cat' to read or the Edit tool to modify files.",
	"vi":     "Use 'cat' to read or the Edit tool to modify files.",
	"nano":   "Use 'cat' to read or the Edit tool to modify files.",
	"emacs":  "Use 'cat' to read or the Edit tool to modify files.",
	"less":   "Use 'cat' to read files directly.",
	"more":   "Use 'cat' to read files directly.",
	"top":    "Use 'ps aux' or 'ps -eo pid,pcpu,pmem,comm --sort=-pcpu | head'.",
	"htop":   "Use 'ps aux' or 'ps -eo pid,pcpu,pmem,comm --sort=-pcpu | head'.",
	"fzf":    "Use grep, find, or other filtering tools instead.",
	"ssh":    "Run commands remotely: 'ssh user@host \"command\"'.",
	"tmux":   "Use background processes or separate sessions.",
	"screen": "Use background processes or separate sessions.",
}

// replCommands are only interactive when invoked without arguments.
var replCommands = map[string]string{
	"python":  "Use 'python script.py' or 'python -c \"code\"'.",
	"python3": "Use 'python3 script.py' or 'python3 -c \"code\"'.",
	"node":    "Use 'node script.js' or 'node -e \"code\"'.",
	"irb":     "Use 'ruby script.rb' or 'ruby -e \"code\"'.",
	"mysql":   "Use 'mysql -e \"SQL\"' or 'mysql < script.sql'.",
	"psql":    "Use 'psql -c \"SQL\"' or 'psql -f script.sql'.",
}

// nonInteractivePatterns are command patterns that indicate non-interactive mode
var nonInteractivePatterns = []string{
	"-y", "--yes", "--assume-yes",
	"-f", "--force",
	"--no-input", "--non-interactive", "--batch",
	"-c ", // for python, ruby, etc. with code argument
	"-e ", // for perl, ruby, etc. with code argument
	"< ",  // input redirection
	"| ",  // piping
}

// BashTool returns the Bash tool definition.
func BashTool() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        "Bash",
		Description: "Executes a bash command. Runs in the working directory (CWD). Timeout defaults to 120s, max 600s.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The command to execute",
				},
				"timeout": map[string]any{
					"type":        "number",
					"description": "Timeout in milliseconds (default 120000, max 600000)",
				},
			},
			"required": []string{"command"},
		},
		Handler:     bashHandler,
		ReadOnly:    false,
		Destructive: false,
	}
}

func bashHandler(input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	command, ok := input["command"].(string)
	if !ok {
		return types.ToolResult{IsError: true}, fmt.Errorf("command is required")
	}

	// Check for interactive commands
	if warning := checkInteractiveCommand(command); warning != "" {
		return types.ToolResult{
			Content: []types.ToolResultContent{{
				Type: "text",
				Text: fmt.Sprintf("⚠️  Interactive command detected:\n%s", warning),
			}},
			IsError: true,
		}, nil
	}

	timeoutMs := 120000
	if t, ok := input["timeout"].(float64); ok {
		timeoutMs = int(t)
	}
	if timeoutMs > 600000 {
		timeoutMs = 600000
	}

	timeout := time.Duration(timeoutMs) * time.Millisecond
	execCtx, cancel := context.WithTimeout(ctx.Ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "bash", "-c", command)
	cmd.Dir = ctx.CWD

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// Combine stdout and stderr
	output := stdout.String()
	if stderr.Len() > 0 {
		if len(output) > 0 {
			output += "\n"
		}
		output += stderr.String()
	}

	isError := false
	if err != nil {
		// Check if it's a timeout
		if execCtx.Err() == context.DeadlineExceeded {
			output += fmt.Sprintf("\nCommand timed out after %dms", timeoutMs)
			output += "\n\nIf this command requires user input or is long-running, consider:"
			output += "\n- Using non-interactive flags (e.g., -y, --batch, --no-input)"
			output += "\n- Running it in the background with '&' and redirecting output"
			output += "\n- Breaking it into smaller, non-interactive steps"
			isError = true
		} else {
			// Non-zero exit code
			isError = true
			if len(output) == 0 {
				output = fmt.Sprintf("Command failed: %v", err)
			}
		}
	}

	return types.ToolResult{
		Content: []types.ToolResultContent{{
			Type: "text",
			Text: output,
		}},
		IsError: isError,
	}, nil
}

// checkInteractiveCommand detects commands that need a TTY or start a REPL.
// Returns a warning string, or "" if the command is safe to run.
func checkInteractiveCommand(command string) string {
	normalized := strings.TrimSpace(command)
	lower := strings.ToLower(normalized)

	parts := strings.Fields(normalized)
	if len(parts) == 0 {
		return ""
	}

	// Extract the base command name, stripping sudo and paths.
	cmdIdx := 0
	if parts[0] == "sudo" && len(parts) > 1 {
		cmdIdx = 1
	}
	baseCmd := parts[cmdIdx]
	if idx := strings.LastIndex(baseCmd, "/"); idx >= 0 {
		baseCmd = baseCmd[idx+1:]
	}

	// Always-interactive commands (vim, top, ssh, etc.) — blocked regardless of args.
	if suggestion, ok := alwaysInteractive[baseCmd]; ok {
		return fmt.Sprintf("Command '%s' requires interactive input.\n\n%s", baseCmd, suggestion)
	}

	// REPL commands — only blocked when invoked bare (no script/args).
	if suggestion, ok := replCommands[baseCmd]; ok {
		hasArgs := len(parts) > cmdIdx+1
		if !hasArgs {
			return fmt.Sprintf("Command '%s' requires interactive input.\n\n%s", baseCmd, suggestion)
		}
		// Has args — allow it (e.g. python script.py, mysql -e "...")
		return ""
	}

	// docker exec -it / docker run -it
	if strings.Contains(lower, "docker exec -it") || strings.Contains(lower, "docker run -it") {
		return "Docker interactive mode (-it) requires a TTY. Use without -it flag."
	}

	// kubectl exec -it
	if strings.Contains(lower, "kubectl exec -it") {
		return "Kubectl interactive mode (-it) requires a TTY. Use without -it flag."
	}

	return ""
}
