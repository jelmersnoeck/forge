# CLI TUI Update - Summary

## What Changed

The Forge CLI has been completely rebuilt as a full-screen Terminal User Interface (TUI) using Bubble Tea.

## Key Features

### 1. Always-Available Input ✨
- Input field is **always visible** at the bottom of the screen
- Type at any time, even while the agent is working
- No more waiting for the agent to finish before typing

### 2. Message Queueing 📬
- Press **Enter** to queue your message
- Messages send automatically when agent is ready
- Queue multiple tasks and let the agent process them sequentially

### 3. Queue Visibility 👀
- See all queued messages above the input field
- **Arrow (→)** shows currently processing message
- **Indented** messages are waiting

### 4. Enhanced Event Display 🎨
- Color-coded events (tools, errors, queue operations)
- Markdown rendering for text responses
- Real-time streaming as agent works

## Visual Layout

```
┌─────────────────────────────────────────────────────────┐
│                                                          │
│               Event Stream Area                          │
│     (Agent responses, tool calls, output)                │
│                                                          │
├─────────────────────────────────────────────────────────┤
│ Queued messages:                                        │
│ → First message (processing)                            │
│   Second message (waiting)                              │
│   Third message (waiting)                               │
├─────────────────────────────────────────────────────────┤
│  ╭─────────────────────────────────────────────────╮    │
│  │ > Type your message...                          │    │
│  ╰─────────────────────────────────────────────────╯    │
└─────────────────────────────────────────────────────────┘
```

## Usage Example

```bash
# Start CLI
just dev-cli

# Type first message
> Create a new authentication module
[Press Enter]

# Message queues and sends immediately
Queued messages:
→ Create a new authentication module

# Agent starts working...
  [Write] auth.go
  
# While agent works, type more messages!
> Add tests for the auth module
[Press Enter]

> Commit the changes when done
[Press Enter]

# Queue shows all pending work
Queued messages:
→ Create a new authentication module
  Add tests for the auth module
  Commit the changes when done

# Agent finishes first task
[done]

# Next message sends automatically
→ Add tests for the auth module

# And so on...
```

## How It Works

### Message Flow
1. User types and presses Enter
2. Message added to internal queue
3. If first in queue: Send to server immediately
4. If queue has items: Wait for current work to finish
5. When agent sends "done" event: Send next queued message
6. Repeat until queue is empty

### Queue State
```javascript
// Internal state
{
  queue: ["msg1", "msg2", "msg3"],  // All messages
  input: "msg4",                     // Currently typing
}

// Display
Queued messages:
→ msg1  (processing - at queue[0])
  msg2  (waiting - at queue[1])
  msg3  (waiting - at queue[2])

> msg4█ (typing - in input buffer)
```

### Event Handling
- CLI subscribes to SSE stream from server
- Events arrive as they happen
- Events rendered in output area
- Special handling for queue-related events

## New Event Types

The CLI now displays these queue-related events:

```javascript
// Agent queues a command
{
  type: "queue_immediate",
  content: "go test ./..."
}
// Displays: ⏱  Queued immediate: go test ./...

// Agent queues completion command
{
  type: "queue_on_complete", 
  content: "git commit -am 'message'"
}
// Displays: ⏱  Queued on complete: git commit -am 'message'

// Queued command executes
{
  type: "queued_task_result",
  content: "[immediate queue] go test\nPASS"
}
// Displays: [queued] [immediate queue] go test...

// Queued command fails
{
  type: "queued_task_error",
  content: "Command failed: ..."
}
// Displays: [queued error] Command failed: ...
```

## Implementation Details

### Technology Stack
- **Bubble Tea** - TUI framework from Charm
- **Lipgloss** - Styling (already in use)
- **Glamour** - Markdown rendering (already in use)

### Architecture
```
┌─────────────────────────────────────────┐
│             Bubble Tea App              │
│                                         │
│  ┌──────────────────────────────────┐   │
│  │           Model State            │   │
│  │  - input: string                 │   │
│  │  - queue: []string               │   │
│  │  - output: []string              │   │
│  │  - renderer: *glamour            │   │
│  └──────────────────────────────────┘   │
│                                         │
│  ┌──────────────────────────────────┐   │
│  │       Update Function            │   │
│  │  - Handle keyboard input         │   │
│  │  - Handle server events (SSE)    │   │
│  │  - Manage queue state            │   │
│  └──────────────────────────────────┘   │
│                                         │
│  ┌──────────────────────────────────┐   │
│  │        View Function             │   │
│  │  - Render output area            │   │
│  │  - Render queue display          │   │
│  │  - Render input field            │   │
│  └──────────────────────────────────┘   │
└─────────────────────────────────────────┘
         ↑                    ↓
         │                    │
      SSE Events          HTTP POST
         │                    │
         └────────────────────┘
              Server
```

### Code Changes

**New file structure:**
```go
// cmd/cli/main.go

type model struct {
    server    string
    sessionID string
    input     string       // Current typing buffer
    queue     []string     // Queued messages
    output    []string     // Event history
    ready     bool
    quitting  bool
    width     int
    height    int
    renderer  *glamour.TermRenderer
    textBuf   strings.Builder
    err       error
}

func (m model) Init() tea.Cmd
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd)
func (m model) View() string
```

**Key functions:**
- `Update()` - Handles all events (keyboard, SSE, errors)
- `View()` - Renders the UI (output + queue + input)
- `handleEvent()` - Processes server events
- `sendMessage()` - HTTP POST to server

## Benefits

### User Experience
- ✅ No waiting - Type anytime
- ✅ Queue multiple tasks - Walk away, come back to finished work
- ✅ Visual feedback - See what's queued
- ✅ Better workflow - More natural interaction

### Developer Experience  
- ✅ Event-driven architecture - Clean separation
- ✅ Reusable components - Bubble Tea patterns
- ✅ Easy to extend - Add more features
- ✅ Better testing - State-based model

## Migration from Old CLI

### Old Behavior
```bash
> First message
[Wait...]
[Wait...]
[Wait...]
[done]

> Second message
[Wait...]
```

**Problems:**
- Blocking input
- Sequential only
- No queue visibility

### New Behavior
```bash
> First message [Enter]
> Second message [Enter]
> Third message [Enter]

Queued messages:
→ First message
  Second message
  Third message

[Agent processes all automatically]
```

**Benefits:**
- Non-blocking input
- Parallel queueing
- Full visibility

## Future Enhancements

Possible improvements:
1. **Edit queue** - Modify queued messages before sending
2. **Delete from queue** - Remove messages
3. **Reorder queue** - Change message order
4. **Multi-line input** - Shift+Enter for newlines
5. **Search output** - Ctrl+F to search
6. **Export transcript** - Save session to file
7. **Queue persistence** - Save queue on exit
8. **Syntax highlighting** - Code blocks in output

## Breaking Changes

**None!** The CLI is backward compatible:
- Same command line arguments (`--resume`)
- Same environment variables (`FORGE_URL`)
- Same server API (no changes)
- Old CLI workflows still work (just better UX)

## Files Modified

### Modified
- `cmd/cli/main.go` - Complete rewrite using Bubble Tea

### Added
- `docs/CLI_TUI.md` - User guide
- `docs/CLI_TUI_VISUAL.md` - Visual examples
- `README.md` - Project overview

### Dependencies
- `go.mod` - Added `github.com/charmbracelet/bubbletea`

## Testing

### Manual Test
```bash
# Terminal 1: Start server
just dev-server

# Terminal 2: Start CLI
just dev-cli

# Type some messages
> Create a test file
> Add some content
> List the directory

# Verify:
# ✅ All messages queue
# ✅ Agent processes sequentially
# ✅ Events display correctly
# ✅ Queue updates properly
```

### Keyboard Test
- Type characters → Should appear in input
- Backspace → Should delete characters
- Enter → Should queue message and clear input
- Ctrl+C → Should exit cleanly

### Event Test
- text → Should render as markdown
- tool_use → Should show tool name + details
- error → Should show in red
- done → Should trigger next queued message
- queue events → Should show with ⏱ indicator

## Performance

- **Responsive** - Updates at 60 FPS (Bubble Tea default)
- **Efficient** - Only re-renders on state changes
- **Scalable** - Handles long conversations (auto-scrolls)
- **Low overhead** - Minimal CPU when idle

## Compatibility

### Terminal Requirements
- ANSI color support (most modern terminals)
- Minimum 80x24 size (works on smaller, but cramped)
- Unicode support (for box-drawing characters)

### Tested Terminals
- ✅ iTerm2
- ✅ Terminal.app (macOS)
- ✅ Alacritty
- ✅ kitty
- ✅ GNOME Terminal
- ✅ tmux
- ✅ screen

## Troubleshooting

### Input not working
- Terminal too small? Resize to at least 80x24
- Key bindings conflict? Try different terminal

### Display garbled
- Terminal encoding? Set to UTF-8
- Terminal type? Try `TERM=xterm-256color`

### Events not showing
- Server running? Check `FORGE_URL`
- SSE connection? Look for connection errors

### Queue not sending
- Check network connection
- Verify server is accessible
- Look for error messages in output

## Documentation

Full documentation available in:
- `docs/CLI_TUI.md` - Comprehensive user guide
- `docs/CLI_TUI_VISUAL.md` - Visual examples and layouts
- `README.md` - Quick start and examples

## Conclusion

The new TUI provides a significantly better user experience with:
- Always-available input
- Visual message queueing
- Real-time event streaming
- Better workflow for rapid development

All while maintaining backward compatibility with existing workflows! 🎉
