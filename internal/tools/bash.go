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

// interactiveCommands maps command names to their non-interactive alternatives or flags
var interactiveCommands = map[string]string{
	"vim":      "Use 'cat' to read or 'echo \"content\" > file' to write. For editing, use the Edit tool.",
	"vi":       "Use 'cat' to read or 'echo \"content\" > file' to write. For editing, use the Edit tool.",
	"nano":     "Use 'cat' to read or 'echo \"content\" > file' to write. For editing, use the Edit tool.",
	"emacs":    "Use 'cat' to read or 'echo \"content\" > file' to write. For editing, use the Edit tool.",
	"less":     "Use 'cat' to read files directly.",
	"more":     "Use 'cat' to read files directly.",
	"top":      "Use 'ps aux' for process listing or 'ps -eo pid,pcpu,pmem,comm --sort=-pcpu | head' for top processes.",
	"htop":     "Use 'ps aux' for process listing or 'ps -eo pid,pcpu,pmem,comm --sort=-pcpu | head' for top processes.",
	"python":   "Use 'python script.py' to run a script, or 'python -c \"code\"' for one-liners.",
	"python3":  "Use 'python3 script.py' to run a script, or 'python3 -c \"code\"' for one-liners.",
	"node":     "Use 'node script.js' to run a script, or 'node -e \"code\"' for one-liners.",
	"irb":      "Use 'ruby script.rb' to run a script, or 'ruby -e \"code\"' for one-liners.",
	"rails":    "Use non-interactive flags like 'rails new app --api --skip-test' or 'rails generate model User name:string --no-interaction'.",
	"npm":      "Use 'npm install' or add flags like 'npm init -y' for non-interactive mode.",
	"yarn":     "Use 'yarn install' or add --non-interactive flag where applicable.",
	"docker":   "Most docker commands are non-interactive, but avoid 'docker attach' or 'docker exec -it'.",
	"ssh":      "SSH requires a TTY. Run commands remotely like: 'ssh user@host \"command\"'.",
	"tmux":     "Tmux requires a TTY. Consider using background processes or separate sessions.",
	"screen":   "Screen requires a TTY. Consider using background processes or separate sessions.",
	"mysql":    "Use 'mysql -e \"SQL\"' for queries, or 'mysql < script.sql' for scripts.",
	"psql":     "Use 'psql -c \"SQL\"' for queries, or 'psql -f script.sql' for scripts.",
	"fzf":      "FZF requires interactive selection. Use grep, find, or other filtering tools instead.",
	"git":      "Git commands work fine, but avoid interactive rebase or commit editors. Use 'git commit -m \"message\"'.",
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

// checkInteractiveCommand detects if a command is likely to be interactive
// and returns a warning with suggestions, or empty string if it's safe to run
func checkInteractiveCommand(command string) string {
	// Normalize command - trim and convert to lowercase for checking
	normalized := strings.TrimSpace(command)
	lower := strings.ToLower(normalized)

	// If command has non-interactive patterns, allow it
	for _, pattern := range nonInteractivePatterns {
		if strings.Contains(lower, pattern) {
			return ""
		}
	}

	// Check for known interactive commands
	// Extract the first command (before pipes, redirects, etc.)
	parts := strings.Fields(normalized)
	if len(parts) == 0 {
		return ""
	}

	firstCmd := parts[0]
	// Remove common prefixes
	firstCmd = strings.TrimPrefix(firstCmd, "sudo")
	firstCmd = strings.TrimSpace(firstCmd)
	if len(strings.Fields(firstCmd)) > 0 {
		firstCmd = strings.Fields(firstCmd)[0]
	}

	// Get base command name (strip path)
	if idx := strings.LastIndex(firstCmd, "/"); idx >= 0 {
		firstCmd = firstCmd[idx+1:]
	}

	// Check against known interactive commands
	if suggestion, found := interactiveCommands[firstCmd]; found {
		return fmt.Sprintf("Command '%s' requires interactive input.\n\n%s", firstCmd, suggestion)
	}

	// Special case: check for common interactive patterns
	if strings.Contains(lower, "docker exec -it") || strings.Contains(lower, "docker run -it") {
		return "Docker interactive mode (-it) requires a TTY.\n\nUse 'docker exec container_name command' without -it flag, or 'docker logs' to view output."
	}

	if strings.Contains(lower, "kubectl exec -it") {
		return "Kubectl interactive mode (-it) requires a TTY.\n\nUse 'kubectl exec pod_name -- command' without -it flag."
	}

	// Check for bare command invocations that start REPLs
	if len(parts) == 1 {
		switch firstCmd {
		case "python", "python3", "node", "irb", "ruby":
			return fmt.Sprintf("Running '%s' without arguments starts an interactive REPL.\n\n%s", firstCmd, interactiveCommands[firstCmd])
		}
	}

	return ""
}
