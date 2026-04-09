package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
)

const (
	// bashIdleTimeout is how long we wait with no output before investigating.
	bashIdleTimeout = 30 * time.Second

	// bashProgressInterval is how often we emit progress events to the TUI.
	bashProgressInterval = 10 * time.Second

	// bashMaxOutputBuffer caps captured output to avoid memory issues.
	bashMaxOutputBuffer = 100 * 1024 // 100KB

	// bashDiagnosticTimeout is the timeout for diagnostic commands (ps, lsof).
	bashDiagnosticTimeout = 5 * time.Second

	// bashWaitDelay gives processes time to exit after SIGTERM before SIGKILL.
	bashWaitDelay = 5 * time.Second
)

// interactiveCommands maps command names to their non-interactive alternatives or flags
var interactiveCommands = map[string]string{
	"vim":     "Use 'cat' to read or 'echo \"content\" > file' to write. For editing, use the Edit tool.",
	"vi":      "Use 'cat' to read or 'echo \"content\" > file' to write. For editing, use the Edit tool.",
	"nano":    "Use 'cat' to read or 'echo \"content\" > file' to write. For editing, use the Edit tool.",
	"emacs":   "Use 'cat' to read or 'echo \"content\" > file' to write. For editing, use the Edit tool.",
	"less":    "Use 'cat' to read files directly.",
	"more":    "Use 'cat' to read files directly.",
	"top":     "Use 'ps aux' for process listing or 'ps -eo pid,pcpu,pmem,comm --sort=-pcpu | head' for top processes.",
	"htop":    "Use 'ps aux' for process listing or 'ps -eo pid,pcpu,pmem,comm --sort=-pcpu | head' for top processes.",
	"python":  "Use 'python script.py' to run a script, or 'python -c \"code\"' for one-liners.",
	"python3": "Use 'python3 script.py' to run a script, or 'python3 -c \"code\"' for one-liners.",
	"node":    "Use 'node script.js' to run a script, or 'node -e \"code\"' for one-liners.",
	"irb":     "Use 'ruby script.rb' to run a script, or 'ruby -e \"code\"' for one-liners.",
	"rails":   "Use non-interactive flags like 'rails new app --api --skip-test' or 'rails generate model User name:string --no-interaction'.",
	"npm":     "Use 'npm install' or add flags like 'npm init -y' for non-interactive mode.",
	"yarn":    "Use 'yarn install' or add --non-interactive flag where applicable.",
	"docker":  "Most docker commands are non-interactive, but avoid 'docker attach' or 'docker exec -it'.",
	"ssh":     "SSH requires a TTY. Run commands remotely like: 'ssh user@host \"command\"'.",
	"tmux":    "Tmux requires a TTY. Consider using background processes or separate sessions.",
	"screen":  "Screen requires a TTY. Consider using background processes or separate sessions.",
	"mysql":   "Use 'mysql -e \"SQL\"' for queries, or 'mysql < script.sql' for scripts.",
	"psql":    "Use 'psql -c \"SQL\"' for queries, or 'psql -f script.sql' for scripts.",
	"fzf":     "FZF requires interactive selection. Use grep, find, or other filtering tools instead.",
	"git":     "Git commands work fine, but avoid interactive rebase or commit editors. Use 'git commit -m \"message\"'.",
}

// nonInteractivePatterns are command patterns that indicate non-interactive mode
var nonInteractivePatterns = []string{
	"-y", "--yes", "--assume-yes",
	"-f", "--force",
	"--no-input", "--non-interactive", "--batch",
	"-c ", // for python, ruby, etc. with code argument
	"-e ", // for perl, ruby, etc. with code argument
	"< ",  // input redirection
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
	command, err := requireString(input, "command")
	if err != nil {
		return types.ToolResult{IsError: true}, err
	}

	if target := commandAccessesEnvFile(command); target != "" {
		return envFileError(target), nil
	}

	if warning := checkInteractiveCommand(command); warning != "" {
		return errResultf("Interactive command detected:\n%s", warning)
	}

	timeoutMs := int(optionalFloat(input, "timeout", 120000))
	if timeoutMs > 600000 {
		timeoutMs = 600000
	}

	timeout := time.Duration(timeoutMs) * time.Millisecond
	execCtx, cancel := context.WithTimeout(ctx.Ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "bash", "-l", "-c", command)
	cmd.Dir = ctx.CWD
	cmd.WaitDelay = bashWaitDelay

	// Process group setup: kill the entire tree on cancel, not just bash.
	setProcGroup(cmd)

	cmd.Env = append(os.Environ(),
		"GIT_EDITOR=true",
		"EDITOR=true",
		"VISUAL=true",
	)

	// Pipe stdout/stderr so we can stream output and detect idle.
	outReader, outWriter := io.Pipe()
	cmd.Stdout = outWriter
	cmd.Stderr = outWriter

	if err := cmd.Start(); err != nil {
		return errResultf("Failed to start command: %v", err)
	}

	// Collect output in a goroutine, signalling on each chunk.
	var (
		outputBuf bytes.Buffer
		outputMu  sync.Mutex
		truncated bool
		outputCh  = make(chan struct{}, 1) // signals new output arrived
	)

	go func() {
		defer func() { _ = outReader.Close() }()
		buf := make([]byte, 4096)
		for {
			n, readErr := outReader.Read(buf)
			if n > 0 {
				outputMu.Lock()
				if outputBuf.Len()+n > bashMaxOutputBuffer {
					truncated = true
					// Keep writing but discard oldest by resetting.
					// In practice we just stop appending.
				} else {
					outputBuf.Write(buf[:n])
				}
				outputMu.Unlock()

				// Signal new output (non-blocking).
				select {
				case outputCh <- struct{}{}:
				default:
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	// Wait for the command in a goroutine so we can select on it.
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
		_ = outWriter.Close() // unblock the reader
	}()

	startTime := time.Now()
	idleTimer := time.NewTimer(bashIdleTimeout)
	defer idleTimer.Stop()
	progressTicker := time.NewTicker(bashProgressInterval)
	defer progressTicker.Stop()
	lastOutputTime := startTime

	emit := ctx.Emit

	for {
		select {
		case err := <-waitDone:
			// Command finished — normal path.
			return bashResult(cmd, err, execCtx, &outputBuf, &outputMu, truncated, timeoutMs)

		case <-outputCh:
			// New output arrived; reset idle timer.
			lastOutputTime = time.Now()
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(bashIdleTimeout)

		case <-progressTicker.C:
			// Periodic TUI progress update.
			if emit != nil {
				elapsed := time.Since(startTime).Round(time.Second)
				idleDur := time.Since(lastOutputTime).Round(time.Second)
				content := fmt.Sprintf("%s (%s elapsed", truncateCommand(command, 60), elapsed)
				if idleDur > 2*time.Second {
					content += fmt.Sprintf(", no output for %s", idleDur)
				}
				content += ")"
				emit(types.OutboundEvent{
					Type:     "tool_progress",
					ToolName: "Bash",
					Content:  content,
				})
			}

		case <-idleTimer.C:
			// No output for bashIdleTimeout. Investigate and report.
			pid := 0
			if cmd.Process != nil {
				pid = cmd.Process.Pid
			}
			diag := gatherDiagnostics(pid)

			outputMu.Lock()
			captured := outputBuf.String()
			outputMu.Unlock()

			var result strings.Builder
			fmt.Fprintf(&result, "Command produced no new output for %s but is still running.\n", bashIdleTimeout)
			if pid > 0 {
				fmt.Fprintf(&result, "PID: %d\n", pid)
			}
			result.WriteString("\n--- Output so far ---\n")
			if truncated {
				result.WriteString("(output truncated to 100KB)\n")
			}
			if captured == "" {
				result.WriteString("(no output)\n")
			} else {
				result.WriteString(captured)
				if !strings.HasSuffix(captured, "\n") {
					result.WriteByte('\n')
				}
			}
			result.WriteString("\n--- Process diagnostics ---\n")
			result.WriteString(diag)
			result.WriteString("\nThe process is still running. You can:\n")
			if pid > 0 {
				fmt.Fprintf(&result, "- Kill it: kill %d\n", pid)
				fmt.Fprintf(&result, "- Check on it: ps -p %d\n", pid)
			}
			result.WriteString("- Use TaskCreate for long-running commands\n")

			if emit != nil {
				emit(types.OutboundEvent{
					Type:     "tool_progress",
					ToolName: "Bash",
					Content:  fmt.Sprintf("%s (idle for %s — returning diagnostics to LLM)", truncateCommand(command, 40), bashIdleTimeout),
				})
			}

			return types.ToolResult{
				Content: []types.ToolResultContent{{
					Type: "text",
					Text: result.String(),
				}},
				IsError: true,
			}, nil
		}
	}
}

// bashResult builds the ToolResult for a completed command.
func bashResult(cmd *exec.Cmd, err error, execCtx context.Context, buf *bytes.Buffer, mu *sync.Mutex, truncated bool, timeoutMs int) (types.ToolResult, error) {
	mu.Lock()
	output := buf.String()
	mu.Unlock()

	if truncated {
		output = "(output truncated to 100KB)\n" + output
	}

	isError := false
	if err != nil {
		switch {
		case execCtx.Err() == context.DeadlineExceeded:
			output += fmt.Sprintf("\nCommand timed out after %dms", timeoutMs)
			output += "\n\nIf this command requires user input or is long-running, consider:"
			output += "\n- Using non-interactive flags (e.g., -y, --batch, --no-input)"
			output += "\n- Using TaskCreate for background execution"
			output += "\n- Breaking it into smaller, non-interactive steps"
			isError = true
		case execCtx.Err() == context.Canceled:
			output += "\nCommand interrupted"
			isError = true
		default:
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

// gatherDiagnostics runs quick ps/lsof checks on the given PID. Each
// diagnostic command has its own timeout so it can't hang us.
func gatherDiagnostics(pid int) string {
	if pid == 0 {
		return "(no PID available)\n"
	}

	var result strings.Builder

	// Process tree
	psOut := runDiagnosticCmd("ps", "-o", "pid,ppid,stat,time,command", "-g", fmt.Sprintf("%d", pid))
	if psOut != "" {
		fmt.Fprintf(&result, "Process tree:\n%s\n", psOut)
	}

	// Listening ports (platform-portable flags)
	lsofOut := runDiagnosticCmd("lsof", "-i", "-P", "-n", "-g", fmt.Sprintf("%d", pid))
	switch {
	case lsofOut != "":
		fmt.Fprintf(&result, "Network connections:\n%s\n", lsofOut)
	default:
		result.WriteString("Network connections: (none detected)\n")
	}

	return result.String()
}

// runDiagnosticCmd executes a command with a short timeout, returning its
// combined output or empty string on failure.
func runDiagnosticCmd(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), bashDiagnosticTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// truncateCommand shortens a command string for display.
func truncateCommand(cmd string, maxLen int) string {
	cmd = strings.TrimSpace(cmd)
	if len(cmd) <= maxLen {
		return cmd
	}
	return cmd[:maxLen-3] + "..."
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

	// Special case: check for common interactive patterns first
	if strings.Contains(lower, "docker exec -it") || strings.Contains(lower, "docker run -it") {
		return "Docker interactive mode (-it) requires a TTY.\n\nUse 'docker exec container_name command' without -it flag, or 'docker logs' to view output."
	}

	if strings.Contains(lower, "kubectl exec -it") {
		return "Kubectl interactive mode (-it) requires a TTY.\n\nUse 'kubectl exec pod_name -- command' without -it flag."
	}

	// Check for known interactive commands
	// Extract the first command (before pipes, redirects, etc.)
	parts := strings.Fields(normalized)
	if len(parts) == 0 {
		return ""
	}

	firstCmd := parts[0]
	// Remove sudo prefix if present
	if firstCmd == "sudo" && len(parts) > 1 {
		firstCmd = parts[1]
	}

	// Get base command name (strip path)
	if idx := strings.LastIndex(firstCmd, "/"); idx >= 0 {
		firstCmd = firstCmd[idx+1:]
	}

	// Commands that ALWAYS need TTY (text editors, pagers, interactive tools)
	alwaysInteractive := map[string]bool{
		"vim": true, "vi": true, "nano": true, "emacs": true,
		"less": true, "more": true,
		"top": true, "htop": true,
		"tmux": true, "screen": true,
		"fzf": true,
		"ssh": true, // Without explicit command
	}

	if alwaysInteractive[firstCmd] {
		if suggestion, found := interactiveCommands[firstCmd]; found {
			return fmt.Sprintf("Command '%s' requires interactive input.\n\n%s", firstCmd, suggestion)
		}
		return fmt.Sprintf("Command '%s' requires interactive input.", firstCmd)
	}

	// Commands that start REPLs when run without arguments
	// But are OK with arguments (scripts, -c flags, etc.)
	replCommands := map[string]bool{
		"python": true, "python3": true,
		"node": true,
		"irb":  true, "ruby": true,
	}

	if replCommands[firstCmd] {
		// Check if it has arguments that make it non-interactive
		hasScript := len(parts) > 1
		if !hasScript {
			// Bare command - will start REPL
			if suggestion, found := interactiveCommands[firstCmd]; found {
				return fmt.Sprintf("Running '%s' without arguments starts an interactive REPL.\n\n%s", firstCmd, suggestion)
			}
		}
		// Has arguments - assume it's running a script or -c/-e code
		return ""
	}

	// Commands like git, docker, npm that are usually OK but can be interactive
	// Only warn if they look suspicious (no clear non-interactive usage)
	conditionallyInteractive := map[string][]string{
		"git":    {"commit", "rebase", "add", "reset"},
		"docker": {"attach", "exec", "run"},
		"npm":    {"init"},
		"yarn":   {"init"},
	}

	if suspiciousSubcommands, found := conditionallyInteractive[firstCmd]; found {
		// Check if any suspicious subcommand is present
		hasSafePattern := false
		for _, part := range parts[1:] {
			// Check for non-interactive flags
			for _, pattern := range nonInteractivePatterns {
				if strings.Contains(strings.ToLower(part), strings.TrimSpace(pattern)) {
					hasSafePattern = true
					break
				}
			}
			// Check for explicit arguments that make it safe
			if strings.HasPrefix(part, "-m") || strings.HasPrefix(part, "--message") {
				hasSafePattern = true
			}
		}

		if hasSafePattern {
			return "" // Has explicit non-interactive flags
		}

		// Check if it has suspicious subcommands without safety flags
		for _, subCmd := range suspiciousSubcommands {
			if len(parts) > 1 && strings.Contains(strings.ToLower(parts[1]), subCmd) {
				// Found suspicious subcommand, check if it looks safe
				// e.g., "docker exec container ls" is safe, "docker exec -it" is not
				if firstCmd == "docker" && subCmd == "exec" {
					commandHasArgs := len(parts) > 3 // command + subcommand + target + actual command
					if commandHasArgs {
						return "" // Has actual command to run
					}
				}

				if firstCmd == "git" && subCmd == "commit" {
					// git commit without -m is interactive
					hasMessage := false
					for _, p := range parts {
						if strings.HasPrefix(p, "-m") || p == "--message" {
							hasMessage = true
							break
						}
					}
					if !hasMessage {
						if suggestion, found := interactiveCommands[firstCmd]; found {
							return fmt.Sprintf("Command '%s' may require interactive input.\n\n%s", firstCmd, suggestion)
						}
					}
				}

				if firstCmd == "git" && (subCmd == "rebase" || subCmd == "add" || subCmd == "reset") {
					// These git commands are only interactive with specific flags
					isInteractive := false
					for _, p := range parts {
						if p == "-i" || p == "--interactive" || p == "-p" || p == "--patch" || p == "-e" || p == "--edit" {
							isInteractive = true
							break
						}
					}
					if isInteractive {
						if suggestion, found := interactiveCommands[firstCmd]; found {
							return fmt.Sprintf("Command '%s' requires interactive input.\n\n%s", firstCmd, suggestion)
						}
					}
					// Non-interactive usage is fine
					return ""
				}

				if (firstCmd == "npm" || firstCmd == "yarn") && subCmd == "init" {
					// npm/yarn init without -y is interactive
					if suggestion, found := interactiveCommands[firstCmd]; found {
						return fmt.Sprintf("Command '%s %s' requires interactive input.\n\n%s", firstCmd, subCmd, suggestion)
					}
					return fmt.Sprintf("Command '%s %s' requires interactive input. Use '%s %s -y' for non-interactive mode.", firstCmd, subCmd, firstCmd, subCmd)
				}
			}
		}
	}

	return ""
}
