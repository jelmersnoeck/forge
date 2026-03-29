# Queue System UX Examples

## Basic Usage

### Example 1: Run tests after each file change

**User:**
```
Create a new authentication module in auth.go with login and logout functions.
Run the tests after each file you create or modify.
```

**Agent Response:**
```
I'll create the authentication module and set up automatic testing.

[tool_use: QueueImmediate]
Setting up: go test ./...

[tool_use: Write] 
Creating auth.go...

[queued_task_execute: Bash]
Running: go test ./...
✓ Tests passed

[tool_use: Write]
Creating auth_test.go...

[queued_task_execute: Bash]
Running: go test ./...
✓ Tests passed

Done! I've created the authentication module with automatic testing.
The tests ran successfully after each file creation.
```

### Example 2: Commit when finished

**User:**
```
Refactor the database connection code to use connection pooling.
When you're done, commit the changes with a descriptive message.
```

**Agent Response:**
```
I'll refactor the database code and commit when complete.

[tool_use: QueueOnComplete]
Queued: git commit -am "Refactor: Add connection pooling to database layer"

[tool_use: Read]
Reading database.go...

[tool_use: Edit]
Updating database.go with connection pooling...

[tool_use: Bash]
Running: go test ./database
✓ Tests passed

[done]
Refactoring complete!

[queued_task_execute: Bash]
Running: git commit -am "Refactor: Add connection pooling to database layer"
✓ Changes committed
```

### Example 3: Multiple queued actions

**User:**
```
Add logging to all HTTP handlers. 
After each file, run the linter.
When everything is done, run the full test suite and commit.
```

**Agent Response:**
```
I'll add logging with automatic linting and final testing.

[tool_use: QueueImmediate]
Queued after each change: golangci-lint run

[tool_use: QueueOnComplete]
Queued on completion: go test ./... && git commit -am "Add logging to HTTP handlers"

[tool_use: Edit]
Updating handlers/user.go...

[queued_task_execute: Bash]
Running: golangci-lint run
✓ Lint passed

[tool_use: Edit]
Updating handlers/auth.go...

[queued_task_execute: Bash]
Running: golangci-lint run
✓ Lint passed

[done]
All handlers updated with logging.

[queued_task_execute: Bash]
Running: go test ./... && git commit -am "Add logging to HTTP handlers"
✓ All tests passed
✓ Changes committed
```

## Advanced Usage

### Example 4: Conditional queuing

**User:**
```
Fix all TODOs in the codebase. For each fix, run the relevant tests.
Only commit at the end if all tests pass.
```

**Agent Response:**
```
I'll work through the TODOs with testing at each step.

[tool_use: Grep]
Finding TODOs...

[tool_use: QueueImmediate]
Queued: go test ./...

[tool_use: QueueOnComplete]
Queued: git commit -am "Fix all TODOs" || echo "Tests failed, not committing"

[tool_use: Edit]
Fixing TODO in auth.go...

[queued_task_execute: Bash]
Running: go test ./...
✓ Tests passed

[tool_use: Edit]
Fixing TODO in database.go...

[queued_task_execute: Bash]
Running: go test ./...
✓ Tests passed

[done]
All TODOs resolved!

[queued_task_execute: Bash]
Running: git commit -am "Fix all TODOs" || echo "Tests failed, not committing"
✓ Changes committed
```

### Example 5: Clearing the queue

**User:**
```
Actually, stop running tests after every change. Just run them at the end.
```

**Agent Response:**
```
I'll clear the immediate queue and set up final testing instead.

[tool_use: QueueImmediate]
command: ""

(This clears the immediate queue)

[tool_use: QueueOnComplete]
Queued on completion: go test ./...

Done! Tests will now only run when all work is complete.
```

## Implementation Notes

### Queue Behavior

**Immediate Queue (`QueueImmediate`):**
- Runs after EVERY tool execution
- Persists until cleared (by queuing empty command)
- Good for: tests, linting, formatting, validation
- Multiple commands run in sequence

**Completion Queue (`QueueOnComplete`):**
- Runs ONCE after the `done` event
- Clears automatically after running
- Good for: commits, deployments, notifications, cleanup
- Multiple commands run in sequence

### Event Flow

```
User Message
  → Agent thinks
  → Agent: QueueImmediate("go test")
  → Agent: Write(file)
    → Immediate queue executes: "go test"
  → Agent: Edit(file)
    → Immediate queue executes: "go test"
  → Agent: No more tool_use
  → Agent: QueueOnComplete execution
  → Agent: Done event
```

### Syntax Patterns Claude Recognizes

Users can say:
- "after each file" / "after every change" → QueueImmediate
- "when done" / "when finished" / "at the end" → QueueOnComplete
- "for each X" → QueueImmediate
- "finally" / "lastly" → QueueOnComplete

### Error Handling

If a queued task fails:
- Immediate queue: Error is logged, queue continues
- Completion queue: Error is logged, remaining tasks still run
- User sees error events in the stream
- Agent's work is NOT blocked by queue failures
