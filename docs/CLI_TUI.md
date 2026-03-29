# CLI TUI - User Guide

## Overview

The Forge CLI now features a full-screen Terminal User Interface (TUI) that allows you to:
- **Always type messages** - Input field is always available
- **Queue messages** - Send multiple messages while the agent is working
- **See what's queued** - Visual display of pending messages
- **Real-time feedback** - Events stream in as they happen

## Layout

```
┌────────────────────────────────────────────┐
│                                             │
│          Event Stream Area                  │
│                                             │
│  Agent responses, tool calls, and output    │
│  scroll here automatically                  │
│                                             │
│                                             │
├─────────────────────────────────────────────┤
│  Queued messages:                           │
│  → First message (currently processing)     │
│    Second message (waiting)                 │
│    Third message (waiting)                  │
├─────────────────────────────────────────────┤
│  ╭───────────────────────────────────────╮  │
│  │ > Type your message here...           │  │
│  ╰───────────────────────────────────────╯  │
└─────────────────────────────────────────────┘
```

## Features

### 1. Always-Available Input
- Input field is **always visible** at the bottom
- Type at any time, even while agent is working
- Press **Enter** to queue your message

### 2. Message Queuing
- Messages are queued and sent one at a time
- First message sends immediately
- Subsequent messages wait for current work to finish
- Queue display shows what's pending

### 3. Queue Display
```
Queued messages:
→ Create a new feature (processing)
  Add tests for the feature (waiting)
  Commit the changes (waiting)
```

- **Arrow (→)** indicates currently processing message
- **Indented** messages are waiting in queue
- Long messages are truncated with `...`

### 4. Enhanced Event Display

**Regular events:**
```
  [Write] src/feature.go
  [Bash] go test ./...
```

**Queue events:**
```
  ⏱  Queued immediate: go test ./...
  ⏱  Queued on complete: git commit -am 'message'
  [queued] [immediate queue] go test ./...
  PASS
```

**Errors:**
```
error: command failed
  [queued error] Command failed: git commit
```

## Usage Examples

### Example 1: Queue Multiple Tasks

```
You type: "Create a new auth module"
[Press Enter - message queues and sends]

Agent starts working...
  [Write] auth.go
  
You type: "Add tests for it"
[Press Enter - message queues, waits for agent]

Queued messages:
→ Create a new auth module
  Add tests for it

Agent finishes first task
[done]

Second message automatically sends
→ Add tests for it

Agent processes second task
  [Write] auth_test.go
[done]

Queue is now empty
```

### Example 2: Queue While Agent Works

```
Agent is refactoring code...
  [Read] database.go
  [Edit] database.go
  
You type: "also add logging"
[Enter]

You type: "and commit when done"
[Enter]

Queued messages:
→ (current work continues)
  also add logging
  and commit when done

Agent finishes current work
[done]

Next message sends automatically
→ also add logging
```

### Example 3: Immediate Feedback

```
You: "Create feature X, test after each file, commit at end"
[Enter]

Agent responds:
  ⏱  Queued immediate: go test ./...
  ⏱  Queued on complete: git commit -am 'Add feature X'
  [Write] feature.go
  [queued] [immediate queue] go test ./...
  PASS
  [Write] feature_test.go
  [queued] [immediate queue] go test ./...
  PASS
[done]
  [queued] [completion queue] git commit -am 'Add feature X'
  [main abc123] Add feature X
```

## Keyboard Shortcuts

- **Enter** - Queue/send message
- **Backspace** - Delete character
- **Ctrl+C** - Exit (shows resume command)
- **Type normally** - Characters appear in input

## Visual Indicators

### Queue Status
- **→** - Currently processing
- **Indented** - Waiting in queue
- **Color: Magenta** - Queue-related messages

### Event Types
- **Blue bold** - Tool use (Write, Bash, etc.)
- **Yellow** - Tool names
- **Red bold** - Errors
- **Green bold** - Prompt (>)
- **Magenta** - Queue operations
- **Gray** - Metadata/hints

## Behavior

### Message Flow
1. Type message and press Enter
2. Message added to queue
3. If queue was empty, message sends immediately
4. If queue had items, message waits
5. When agent finishes (sends "done" event), next message in queue sends
6. Repeat until queue is empty

### Queue Persistence
- Queue is **in-memory only** (not saved between CLI sessions)
- On Ctrl+C, queued messages are lost
- To resume session: `forge-cli --resume <session-id>`

### Scrolling
- Output area scrolls automatically
- Shows last N lines that fit on screen
- New events appear at the bottom
- Old events scroll off the top

## Advanced Usage

### Rapid Task Queuing
```bash
# Queue 5 tasks quickly
forge-cli

> Create module A
> Create module B  
> Create module C
> Test everything
> Commit all changes

# All 5 messages queued
# Agent processes them one by one
```

### Interactive Refinement
```bash
# Start a task
> Refactor the database code

# While agent is working, add refinements
> also add connection pooling
> and improve error handling

# Agent incorporates feedback as it goes
```

### Queue + Agent Queues
```bash
# Message 1: Set up queues
> Create feature X, test after each file

# Message 2: More work (queued in CLI)
> Also add documentation

# Agent has immediate queue: "go test"
# CLI has message queue: ["message 1", "message 2"]
# Both work together seamlessly
```

## Comparison: Old vs New

### Old CLI
```
> Create feature A
[Wait for agent to finish...]
[Wait...]
[Wait...]
[done]

> Create feature B
[Wait...]
```

**Problems:**
- Must wait for agent before typing
- Can't queue multiple tasks
- No visibility into what's pending

### New TUI
```
> Create feature A [Enter]
> Create feature B [Enter]
> Create feature C [Enter]

Queued messages:
→ Create feature A
  Create feature B
  Create feature C

[Agent processes all three automatically]
```

**Benefits:**
- ✅ Type anytime
- ✅ Queue multiple tasks
- ✅ See what's pending
- ✅ Better UX

## Technical Details

### Implementation
- Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea)
- Full-screen alternate buffer (clean terminal on exit)
- Event-driven architecture
- Non-blocking input handling

### State Management
- **Input** - Current typing buffer
- **Queue** - Array of pending messages
- **Output** - Array of event strings
- **Server Events** - Streamed via SSE

### Event Flow
```
User types + Enter
  ↓
Message added to queue
  ↓
If first in queue: HTTP POST to server
  ↓
Server/Agent process message
  ↓
SSE events stream back
  ↓
Events rendered in output area
  ↓
"done" event received
  ↓
If queue has more: send next message
  ↓
Repeat
```

## Troubleshooting

### Input not working
- Make sure terminal supports TUI (most modern terminals do)
- Try resizing terminal window
- Check for conflicting key bindings

### Queue not sending
- Verify server is running: `just dev-server`
- Check network connection to `FORGE_URL`
- Look for error messages in output

### Display issues
- Terminal too small: Resize to at least 80x24
- Colors not showing: Terminal may not support ANSI colors
- Text garbled: Try a different terminal emulator

### Events not appearing
- Check SSE connection in output
- Verify session ID is valid
- Restart CLI: `forge-cli --resume <session-id>`

## Examples in Action

### Scenario: Rapid Prototyping
```bash
# Queue up a full feature quickly
> Create user authentication module
> Add login and logout endpoints  
> Create tests for auth module
> Add password hashing with bcrypt
> Create database migrations
> Commit everything

Queued messages:
→ Create user authentication module
  Add login and logout endpoints
  Create tests for auth module
  Add password hashing with bcrypt
  Create database migrations
  Commit everything

# Walk away, come back to completed work
```

### Scenario: Iterative Development
```bash
# Start work
> Implement JWT token generation

# Agent is working...
  [Write] jwt.go

# You realize something
> make sure to include refresh tokens too

Queued messages:
→ Implement JWT token generation
  make sure to include refresh tokens too

# Agent finishes first task, picks up your addition
```

### Scenario: Review and Adjust
```bash
# Agent is refactoring
  [Read] api.go
  [Edit] api.go
  
# You notice output and want changes
> also update the error messages to be more descriptive
> and add request logging

Queued messages:
  also update the error messages to be more descriptive
  and add request logging

# Agent will handle these after current work
```

## Future Enhancements

Possible improvements:
- Edit queued messages (arrow keys to select, edit)
- Delete from queue (Ctrl+D to remove)
- Reorder queue (drag/drop or shortcuts)
- Save queue to file (persist across sessions)
- Queue history (see what was queued before)
- Multi-line input (Shift+Enter for newline)
- Search output (Ctrl+F)
- Export session transcript
