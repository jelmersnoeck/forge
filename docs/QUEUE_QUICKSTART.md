# Queue System - Quick Start

## Summary

The agent now supports **two queue types** for running commands at specific times:

- **`QueueImmediate`** - Runs after EVERY tool execution
- **`QueueOnComplete`** - Runs ONCE when all work is done

## How Users Interact

Users simply ask in natural language. Claude recognizes the intent and uses the queue tools:

```
✅ "test after each file change"
✅ "check the linter after every edit"  
✅ "commit when you're done"
✅ "deploy after all changes"
```

## Real-World Examples

### Example 1: Test-Driven Development
```
User: "Create a calculator module with add and multiply functions. 
       Run tests after each file you create."

Claude's Actions:
1. Calls QueueImmediate("go test ./calculator")
2. Creates calculator.go
   → Tests run automatically
3. Creates calculator_test.go
   → Tests run automatically
4. Reports: "✓ Calculator module created, all tests passing"
```

### Example 2: Auto-Commit
```
User: "Refactor the auth code to use bcrypt. 
       When done, commit with a good message."

Claude's Actions:
1. Calls QueueOnComplete("git commit -am 'Refactor: Switch to bcrypt for password hashing'")
2. Edits auth.go
3. Updates tests
4. Verifies everything works
5. On completion: git commit runs automatically
```

### Example 3: Both Queues
```
User: "Add request logging to all API handlers.
       Format the code after each file.
       When finished, run the full test suite and commit."

Claude's Actions:
1. Calls QueueImmediate("gofmt -w .")
2. Calls QueueOnComplete("go test ./... && git commit -am 'Add request logging'")
3. Edits handler1.go → gofmt runs
4. Edits handler2.go → gofmt runs
5. Edits handler3.go → gofmt runs
6. On completion: Tests run, then commit
```

## What You See (Event Stream)

```
[text] I'll add logging and set up automatic formatting...

[tool_use: QueueImmediate] 
Command: gofmt -w .

[queue_immediate]
✓ Queued to run after each tool: gofmt -w .

[tool_use: Edit]
Updating handlers/user.go...

[queued_task_result]
[immediate queue] gofmt -w .
Formatted 1 file

[tool_use: Edit]
Updating handlers/auth.go...

[queued_task_result]
[immediate queue] gofmt -w .
Formatted 1 file

[done]
All handlers updated with logging.
```

## Implementation Files

### New Files Created
- `internal/tools/queue_immediate.go` - QueueImmediate tool
- `internal/tools/queue_on_complete.go` - QueueOnComplete tool
- `docs/QUEUE_SYSTEM.md` - Full implementation docs
- `docs/QUEUE_UX_EXAMPLES.md` - UX examples and patterns
- `docs/QUEUE_DIAGRAMS.md` - Visual flow diagrams

### Modified Files
- `internal/agent/hub.go` - Added queue storage and management
- `internal/agent/worker.go` - Added queue execution logic
- `internal/tools/registry.go` - Registered new queue tools

## How It Works Internally

### Architecture Flow
```
1. User asks to "test after each change"
   ↓
2. Claude calls QueueImmediate tool
   ↓
3. Tool emits "queue_immediate" event
   ↓
4. Worker intercepts event, adds to Hub.immediateQueue
   ↓
5. Claude calls Write/Edit/etc tool
   ↓
6. Tool emits "tool_use" event
   ↓
7. Worker intercepts, runs all commands in immediateQueue
   ↓
8. Results emitted as "queued_task_result" events
   ↓
9. Loop continues until done
   ↓
10. Worker intercepts "done" event
   ↓
11. Worker runs all commands in completionQueue
   ↓
12. Results emitted as "queued_task_result" events
   ↓
13. "done" event propagates to user
```

### Key Design Points

1. **Event-based**: Uses existing event pub/sub system
2. **Non-intrusive**: No changes to conversation loop core logic
3. **Thread-safe**: Queue management uses mutexes
4. **Transparent**: All queue actions visible in event stream
5. **Resilient**: Queue failures don't block agent work

## Testing

### Build and Verify
```bash
cd /Users/jelmersnoeck/Projects/forge
go build ./...
go vet ./...
```

### Manual Test
```bash
# Start server
just dev-server

# In another terminal, start CLI
just dev-cli

# Send a test message
> Create a test.txt file with "Hello".
  After creating it, run "cat test.txt".
  When done, echo "All finished!"

# Expected output:
# - Agent creates file
# - "cat test.txt" runs (shows "Hello")
# - "echo All finished!" runs
# - Done
```

## Common Patterns

### Pattern: Continuous Testing
```
"test after each change"
"run the test suite after every file"
"check if it compiles after each edit"
```
→ Claude uses `QueueImmediate` with test command

### Pattern: Final Actions
```
"commit when done"
"deploy after all changes"
"send a notification when finished"
"clean up temporary files at the end"
```
→ Claude uses `QueueOnComplete` with the command

### Pattern: Clear Queue
```
"stop running tests automatically"
"don't format anymore"
```
→ Claude uses `QueueImmediate("")` with empty string

### Pattern: Multi-Step Cleanup
```
"when done: run tests, then commit if they pass, then deploy"
```
→ Claude uses `QueueOnComplete("go test && git commit && ./deploy.sh")`

## Configuration

No configuration needed! The queue tools are automatically registered when the agent starts.

### Environment Variables
- No new environment variables
- Uses existing `WORKSPACE_DIR` for Bash command execution
- Queue commands run in the agent's working directory

## Limitations

### Current Limitations
1. **No cancellation**: Once queued, commands run (except immediate queue can be cleared)
2. **Bash only**: Only supports Bash commands, not other tools
3. **No conditionals**: Can't do "commit only if tests pass" (use shell && instead)
4. **No persistence**: Queues don't survive agent restart
5. **No inspection**: No tool to list current queue contents

### Workarounds
```bash
# Conditional execution (use shell features)
QueueOnComplete("go test && git commit -am 'message'")

# Multiple commands
QueueOnComplete("make test && make build && make deploy")

# Notifications
QueueOnComplete("notify-send 'Build complete!' || echo 'Done'")
```

## Future Enhancements

Possible improvements:
- Queue inspection tool (`ListQueues`)
- Clear completion queue tool
- Tool-based queuing (queue any tool, not just Bash)
- Conditional execution based on previous results
- Queue persistence across sessions
- Priority/ordering control

## Troubleshooting

### Queue not running
- Check event stream for `queue_immediate` or `queue_on_complete` events
- Verify Bash tool is working: `just dev-cli` → "run echo test"
- Check agent logs for "executing immediate queue" or "executing completion queue"

### Commands failing
- Check `queued_task_error` events in stream
- Verify command works in agent's CWD
- Check agent logs for detailed error messages

### Immediate queue running too often
- Clear it: Ask agent to "stop running X automatically"
- Claude will call `QueueImmediate("")`

## Documentation

- **Full implementation**: `/docs/QUEUE_SYSTEM.md`
- **UX examples**: `/docs/QUEUE_UX_EXAMPLES.md`
- **Visual diagrams**: `/docs/QUEUE_DIAGRAMS.md`
- **This guide**: `/docs/QUEUE_QUICKSTART.md`

## Support

Queue system is integrated into the existing agent architecture. No breaking changes to existing functionality.

For questions or issues, refer to the detailed documentation in `/docs/QUEUE_*.md`.
