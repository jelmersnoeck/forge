# Interactive Command Handling

## Problem

Interactive commands (like `vim`, `less`, `python` REPL, `npm init`, etc.) block the conversation loop because:

1. The Bash tool uses `exec.Command().Run()` which blocks until the command completes
2. Stdin is not connected to the command, so interactive prompts hang waiting for input
3. The command either:
   - Times out after 120s (default) or 600s (max)
   - Fails immediately with "not a terminal" errors
   - Blocks indefinitely if it doesn't check for TTY

This prevents the agent from continuing the conversation and causes poor user experience.

## Solution

The Bash tool now **detects and rejects interactive commands** before execution, providing helpful suggestions for non-interactive alternatives.

### Detection Logic

The tool maintains:

1. **Known Interactive Commands**: A map of command names to suggested alternatives
   - Text editors: `vim`, `vi`, `nano`, `emacs` → "Use the Edit tool or cat/echo"
   - Pagers: `less`, `more` → "Use cat"
   - REPLs: `python`, `node`, `irb` (when bare) → "Use script files or -c/-e flags"
   - Process monitors: `top`, `htop` → "Use ps aux"
   - Package managers: `npm init`, `rails generate` → "Use non-interactive flags like -y, --batch"

2. **Non-Interactive Patterns**: Flags/patterns that indicate safe execution
   - `-y`, `--yes`, `--assume-yes`
   - `-c`, `-e` (code execution flags)
   - `< ` (input redirection)
   - `| ` (piping)
   - `--no-input`, `--batch`, `--non-interactive`

3. **Special Cases**: Context-aware detection
   - `docker exec -it` vs `docker exec` (without `-it`)
   - `kubectl exec -it` vs `kubectl exec`
   - Bare REPL commands (`python` alone) vs scripts (`python script.py`)

### How It Works

```go
func checkInteractiveCommand(command string) string {
    // 1. Check for non-interactive patterns - allow if found
    if hasNonInteractivePattern(command) {
        return "" // empty = allowed
    }
    
    // 2. Extract first command name (handle sudo, paths)
    firstCmd := extractCommandName(command)
    
    // 3. Check against known interactive commands
    if suggestion, found := interactiveCommands[firstCmd]; found {
        return fmt.Sprintf("Command '%s' requires interactive input.\n\n%s", 
            firstCmd, suggestion)
    }
    
    // 4. Check for bare REPL invocations
    if isBareREPL(command) {
        return "Starting interactive REPL. Use script files instead."
    }
    
    return "" // allowed
}
```

### Examples

#### ❌ Blocked Commands

```bash
# Text editors
vim file.txt
nano config.ini

# Pagers
less logfile.txt
more output.txt

# Bare REPLs
python
node
irb

# Interactive package managers
npm init
rails generate model User

# TTY-required commands
docker exec -it container bash
ssh user@host
tmux attach
top
```

**Response**: Error with helpful suggestion about non-interactive alternatives.

#### ✅ Allowed Commands

```bash
# With non-interactive flags
npm init -y
rails generate model User --no-interaction

# Script execution
python script.py
node app.js
ruby deploy.rb

# Code execution flags
python -c "print('hello')"
node -e "console.log('hello')"

# Piping and redirection
python < input.py
cat file.txt | grep pattern
vim file.txt | cat  # pipe overrides interactive detection

# Non-interactive docker
docker exec container ls
docker logs container

# File operations
cat file.txt
echo "content" > file.txt
tail -f logfile.txt
```

### Error Messages

When an interactive command is detected, the tool returns:

```
⚠️  Interactive command detected:
Command 'vim' requires interactive input.

Use 'cat' to read or 'echo "content" > file' to write. For editing, use the Edit tool.
```

For timeout scenarios (if detection is bypassed), enhanced error messages:

```
Command timed out after 120000ms

If this command requires user input or is long-running, consider:
- Using non-interactive flags (e.g., -y, --batch, --no-input)
- Running it in the background with '&' and redirecting output
- Breaking it into smaller, non-interactive steps
```

## Benefits

1. **Immediate Feedback**: Users get instant, actionable suggestions instead of waiting for timeouts
2. **Better UX**: Clear error messages explain why the command won't work
3. **Prevents Blocking**: The conversation loop never blocks on interactive commands
4. **Educational**: Teaches users about non-interactive alternatives
5. **Flexible**: Commands with non-interactive flags are automatically allowed

## Future Enhancements

### Option 1: Background Job Support
- Add a `QueueBackground` tool for long-running commands
- Run in separate tmux pane with job ID
- Allow monitoring via `GetJobStatus` tool
- Support `GetJobOutput` and `StopJob`

### Option 2: Input Streaming
- For commands that need stdin but aren't truly interactive
- Stream input via tool parameter
- Handle commands like `mysql < script.sql` more explicitly

### Option 3: User Override
- Add `force_interactive: true` parameter
- Warn but execute anyway
- Require explicit timeout setting

## Testing

The implementation includes comprehensive tests:

```bash
# Run interactive command tests
go test ./internal/tools -run TestBashToolInteractive -v

# Run detection logic tests
go test ./internal/tools -run TestCheckInteractive -v
```

Test coverage includes:
- Known interactive commands (vim, less, python REPL, etc.)
- Non-interactive alternatives (python script.py, npm init -y, etc.)
- Edge cases (sudo, piping, redirection, docker -it, etc.)
- Timeout improvements with better error messages

## Configuration

Currently, the interactive command list is hardcoded. Future versions could:
- Allow configuration via `.forge/config.yml`
- Support user-defined interactive commands
- Allow allowlist/denylist per project

## Related Tools

- **Edit Tool**: Recommended alternative for file editing
- **Read Tool**: Alternative for viewing file contents
- **Write Tool**: Alternative for creating files
- **QueueImmediate**: For running commands after tool execution
- **QueueOnComplete**: For running commands after conversation completes
