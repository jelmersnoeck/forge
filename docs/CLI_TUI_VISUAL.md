# CLI TUI - Visual Guide

## Screen Layout

```
┌─────────────────────────────────────────────────────────────────┐
│ forge cli — session abc-123                                      │
│ server: http://localhost:3000                                    │
│                                                                   │
│ Press Ctrl+C to exit. Session: forge-cli --resume abc-123       │
│                                                                   │
│ I'll create a new authentication module with login and logout.   │
│                                                                   │
│   [Write] auth.go                                                │
│   [Bash] go test ./...                                           │
│                                                                   │
│   PASS                                                            │
│   ok    github.com/example/auth   0.123s                        │
│                                                                   │
│   [Write] auth_test.go                                           │
│                                                                   │
│ Done! I've created the authentication module.                    │
│                                                                   │
├─────────────────────────────────────────────────────────────────┤
│ Queued messages:                                                 │
│   Add password hashing with bcrypt                               │
│   Add rate limiting to login endpoint                            │
├─────────────────────────────────────────────────────────────────┤
│  ╭───────────────────────────────────────────────────────────╮  │
│  │ > Create database migrations                              │  │
│  ╰───────────────────────────────────────────────────────────╯  │
└─────────────────────────────────────────────────────────────────┘
```

## Queue States

### Empty Queue
```
╭───────────────────────────────────────────────────────────╮
│ > Type your message...                                    │
╰───────────────────────────────────────────────────────────╯
```

### One Message (Processing)
```
Queued messages:
→ Create a new feature

╭───────────────────────────────────────────────────────────╮
│ > Add tests                                               │
╰───────────────────────────────────────────────────────────╯
```

### Multiple Messages
```
Queued messages:
→ Create a new feature (processing - arrow indicator)
  Add tests (waiting)
  Commit changes (waiting)

╭───────────────────────────────────────────────────────────╮
│ > Type another message...                                 │
╰───────────────────────────────────────────────────────────╯
```

### Long Messages
```
Queued messages:
→ Create a comprehensive authentication system with JWT tokens, refresh...
  Add extensive test coverage including unit tests, integration tests, a...
  Update all documentation to reflect the new authentication system and...

╭───────────────────────────────────────────────────────────╮
│ >                                                         │
╰───────────────────────────────────────────────────────────╯
```

## Event Display Examples

### Text Response
```
I'll help you create that feature. Let me start by analyzing the 
existing code structure.

The current implementation uses a simple approach. I'll refactor it
to use a more robust pattern.
```

### Tool Calls
```
  [Read] src/main.go
  [Edit] src/main.go
  [Bash] go build ./...
  [Write] src/feature.go
  [Grep] pattern="TODO"
```

### Tool Calls with Details
```
  [Bash] go test ./...
  [Write] config/settings.yaml
  [Edit] README.md
  [Grep] pattern="TODO" path="src/"
```

### Queue Operations
```
  ⏱  Queued immediate: go test ./...
  ⏱  Queued on complete: git commit -am 'Add feature'
```

### Queued Task Execution
```
  [queued] [immediate queue] go test ./...
  PASS
  ok    github.com/example/app   0.234s

  [queued] [completion queue] git commit -am 'Add feature'
  [main abc123] Add feature
   3 files changed, 150 insertions(+), 20 deletions(-)
```

### Errors
```
error: compilation failed

  [queued error] Command failed: go build
  Error: undefined: InvalidFunction
```

## Color Scheme

```
forge cli                    → Blue Bold (header)
— session abc-123            → Gray (dim)

I'll create...               → White (normal text)

  [Write]                    → Yellow Bold (tool name)
  auth.go                    → Gray (tool detail)

error: failed               → Red Bold (error)

Queued messages:            → Magenta Bold (queue header)
→ First message             → Magenta (queue item - processing)
  Second message            → Magenta (queue item - waiting)

> Type here...              → Green Bold (prompt)
Type your message...        → Gray (placeholder)
```

## Interactive Flow

### Step 1: User Types
```
╭───────────────────────────────────────────────────────────╮
│ > Create auth module█                                     │
╰───────────────────────────────────────────────────────────╯
```

### Step 2: User Presses Enter
```
Queued messages:
→ Create auth module

╭───────────────────────────────────────────────────────────╮
│ >                                                         │
╰───────────────────────────────────────────────────────────╯
```

### Step 3: Agent Responds
```
I'll create an authentication module.

  [Write] auth.go

Queued messages:
→ Create auth module

╭───────────────────────────────────────────────────────────╮
│ > Add tests█                                              │
╰───────────────────────────────────────────────────────────╯
```

### Step 4: User Adds More
```
I'll create an authentication module.

  [Write] auth.go

Queued messages:
→ Create auth module
  Add tests

╭───────────────────────────────────────────────────────────╮
│ >                                                         │
╰───────────────────────────────────────────────────────────╯
```

### Step 5: First Task Completes
```
I'll create an authentication module.

  [Write] auth.go

Done! Authentication module created.

Queued messages:
→ Add tests

╭───────────────────────────────────────────────────────────╮
│ >                                                         │
╰───────────────────────────────────────────────────────────╯
```

### Step 6: Second Task Processes
```
I'll create an authentication module.

  [Write] auth.go

Done! Authentication module created.

I'll add comprehensive tests.

  [Write] auth_test.go

╭───────────────────────────────────────────────────────────╮
│ >                                                         │
╰───────────────────────────────────────────────────────────╯
```

## Real-World Example

### Complete Session
```
┌─────────────────────────────────────────────────────────────────┐
│ forge cli — session 8f7d3a2b                                     │
│ server: http://localhost:3000                                    │
│                                                                   │
│ I'll refactor the database code to use connection pooling.       │
│                                                                   │
│   [Read] database.go                                             │
│   [Edit] database.go                                             │
│                                                                   │
│ I've added connection pooling with the following configuration:  │
│                                                                   │
│ ```go                                                             │
│ MaxOpenConns: 25                                                 │
│ MaxIdleConns: 5                                                  │
│ ConnMaxLifetime: 5 * time.Minute                                │
│ ```                                                               │
│                                                                   │
│   ⏱  Queued immediate: go test ./database                       │
│   [Write] database_test.go                                       │
│   [queued] [immediate queue] go test ./database                  │
│                                                                   │
│   PASS                                                            │
│   ok    myapp/database   0.456s                                 │
│                                                                   │
│   ⏱  Queued on complete: git commit -am 'Add connection pool'   │
│                                                                   │
│   [queued] [completion queue] git commit -am 'Add connection...' │
│   [main 7f8d9a] Add connection pooling to database              │
│    2 files changed, 85 insertions(+), 12 deletions(-)           │
│                                                                   │
├─────────────────────────────────────────────────────────────────┤
│  ╭───────────────────────────────────────────────────────────╮  │
│  │ > Update the documentation                                │  │
│  ╰───────────────────────────────────────────────────────────╯  │
└─────────────────────────────────────────────────────────────────┘
```

## Mobile/Small Terminal

### Compact Layout (80x24)
```
┌────────────────────────────────────────────────────────┐
│ forge cli — 8f7d3a2b                                    │
│                                                         │
│ Creating authentication module...                       │
│                                                         │
│   [Write] auth.go                                       │
│                                                         │
│ Done!                                                   │
│                                                         │
├────────────────────────────────────────────────────────┤
│ Queued:                                                │
│ → Add tests                                            │
│   Commit                                               │
├────────────────────────────────────────────────────────┤
│  ╭──────────────────────────────────────────────────╮  │
│  │ > Add docs                                       │  │
│  ╰──────────────────────────────────────────────────╯  │
└────────────────────────────────────────────────────────┘
```

## ASCII Art Representation

### Full UI Flow
```
     INPUT ALWAYS AVAILABLE
            ↓
    ┌───────────────┐
    │  User types   │
    │  Press Enter  │
    └───────┬───────┘
            ↓
    ┌───────────────┐
    │ Add to Queue  │
    └───────┬───────┘
            ↓
       Is queue = 1?
       ↙          ↘
     YES          NO
      ↓            ↓
  Send now    Wait in queue
      ↓            ↓
  ┌─────────┐     ↓
  │ Agent   │     ↓
  │ Process │     ↓
  └────┬────┘     ↓
       ↓          ↓
   [Events]       ↓
       ↓          ↓
    Display       ↓
       ↓          ↓
    [done]        ↓
       ↓          ↓
   Queue > 1? ───┘
       ↓
    Send next
       ↓
      Loop
```

### Queue Visualization
```
User Input:       Queue State:           Agent State:

Message 1  ────→  [Message 1] ────────→  Processing...
                                               ↓
Message 2  ────→  [Message 2]                Events
                                               ↓
Message 3  ────→  [Message 3]               [done]
                                               ↓
                  [Message 2] ────────→  Processing...
                                               ↓
                  [Message 3]                Events
                                               ↓
                                             [done]
                                               ↓
                  [Message 3] ────────→  Processing...
                                               ↓
                  [ empty ]                  Events
                                               ↓
                                             [done]
```

## Keyboard Input Examples

### Typing
```
> C█                         (typed 'C')
> Cr█                        (typed 'r')
> Cre█                       (typed 'e')
> Crea█                      (typed 'a')
> Creat█                     (typed 't')
> Create█                    (typed 'e')
> Create █                   (typed space)
> Create f█                  (typed 'f')
> Create feature█            (typed 'eature')
```

### Backspace
```
> Create feature█            (full text)
> Create featur█             (backspace)
> Create featu█              (backspace)
> Create feat█               (backspace)
```

### Enter
```
> Create feature█            (before Enter)

Queued messages:             (after Enter - queued)
→ Create feature

>                            (input cleared)
```

## Exit Behavior

### Normal Exit (Ctrl+C)
```
To resume: forge-cli --resume 8f7d3a2b
```

### With Queued Messages
```
Warning: 2 messages in queue will be lost
To resume: forge-cli --resume 8f7d3a2b
```
(Note: Current implementation doesn't warn - future enhancement)
