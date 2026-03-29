# Queue System Implementation

## Overview

The agent now supports two types of queues for deferred command execution:

1. **Immediate Queue** - Commands that run after EVERY tool execution
2. **Completion Queue** - Commands that run ONCE when all work is done

## User Experience

Users interact with queues through natural language. Claude recognizes patterns and uses the queue tools automatically.

### Examples

```bash
# User asks
"Create a feature and run tests after each change"

# Claude does
1. Uses QueueImmediate tool to queue "go test ./..."
2. Creates files with Write/Edit tools
3. After EACH file operation, "go test" runs automatically
4. User sees test results in the event stream
```

```bash
# User asks
"Refactor the code, and when done commit with a good message"

# Claude does
1. Uses QueueOnComplete tool to queue git commit
2. Performs refactoring with multiple tool calls
3. When no more work needed (done event), commit runs
4. User sees commit result
```

## Implementation Details

### New Tools

#### `QueueImmediate`
Queues a bash command to run after each subsequent tool execution.

**Schema:**
```json
{
  "name": "QueueImmediate",
  "description": "Queue a bash command to run immediately after the next tool execution...",
  "input_schema": {
    "type": "object",
    "properties": {
      "command": {
        "type": "string",
        "description": "The bash command to execute after each tool call"
      },
      "description": {
        "type": "string",
        "description": "Human-readable description (optional)"
      }
    },
    "required": ["command"]
  }
}
```

**Behavior:**
- Command persists across tool executions
- Runs after EVERY tool call until cleared
- Clear by calling with empty `command: ""`
- Multiple commands can be queued (run in sequence)

**Example:**
```javascript
// Claude calls this tool
{
  "name": "QueueImmediate",
  "input": {
    "command": "go test ./...",
    "description": "Run tests after each file change"
  }
}

// Result appears in tool result
"✓ Queued to run after each tool: go test ./...
  Reason: Run tests after each file change"
```

#### `QueueOnComplete`
Queues a bash command to run once when all work is complete.

**Schema:**
```json
{
  "name": "QueueOnComplete",
  "description": "Queue a bash command to run once all current work is complete...",
  "input_schema": {
    "type": "object",
    "properties": {
      "command": {
        "type": "string",
        "description": "The bash command to execute when work is complete"
      },
      "description": {
        "type": "string",
        "description": "Human-readable description (optional)"
      }
    },
    "required": ["command"]
  }
}
```

**Behavior:**
- Command runs once after the `done` event
- Queue is cleared after execution
- Multiple commands can be queued (run in sequence)
- Good for: commits, deployments, notifications, cleanup

### Architecture

#### Event Flow
```
User sends message
  ↓
Worker receives message
  ↓
Loop runs (Send/Resume)
  ↓
Claude responds with tool_use
  ↓
  [If tool_use is QueueImmediate]
    → Event type: "queue_immediate"
    → Worker intercepts, adds to Hub.immediateQueue
  ↓
  [If tool_use is QueueOnComplete]
    → Event type: "queue_on_complete"
    → Worker intercepts, adds to Hub.completionQueue
  ↓
  [If tool_use is any other tool]
    → Tool executes normally
    → Event type: "tool_use" emitted
    → Worker intercepts, runs Hub.immediateQueue commands
    → Results emitted as "queued_task_result" events
  ↓
Loop continues until no more tool_use blocks
  ↓
Event type: "done" about to be emitted
  ↓
Worker intercepts, runs Hub.completionQueue commands
  ↓
Results emitted as "queued_task_result" events
  ↓
"done" event emitted
```

#### Code Structure

**Hub (internal/agent/hub.go)**
- Stores two queues: `immediateQueue []string`, `completionQueue []string`
- Thread-safe queue management with mutex
- Methods:
  - `EnqueueImmediate(command string)` - Add to immediate queue (clear if empty string)
  - `EnqueueCompletion(command string)` - Add to completion queue
  - `GetImmediateQueue() []string` - Get copy (doesn't clear)
  - `PullCompletionQueue() []string` - Get and clear

**Worker (internal/agent/worker.go)**
- Intercepts events in the `emit` function
- Detects queue management events: `queue_immediate`, `queue_on_complete`
- Detects execution events: `tool_use`, `done`
- Executes queued commands using the Bash tool
- Emits results as `queued_task_result` or `queued_task_error` events

**Tools (internal/tools/queue_*.go)**
- `QueueImmediateTool` - Emits `queue_immediate` event
- `QueueOnCompleteTool` - Emits `queue_on_complete` event
- Registered in `NewDefaultRegistry()`

### Event Types

The system emits new event types:

```typescript
// Tool emits these when called
{
  type: "queue_immediate",
  content: "go test ./...",
  toolName: "Bash"
}

{
  type: "queue_on_complete", 
  content: "git commit -am 'message'",
  toolName: "Bash"
}

// Worker emits these after executing queued commands
{
  type: "queued_task_result",
  content: "[immediate queue] go test ./...\n<command output>",
  toolName: "Bash"
}

{
  type: "queued_task_error",
  content: "[completion queue] Command failed: ...\nError: ...",
  toolName: "Bash"
}
```

## Usage Patterns

### Pattern 1: Testing After Changes
```
User: "Add logging to all handlers and test after each change"

Claude:
1. QueueImmediate("go test ./handlers/...")
2. Edit(handler1.go)
   → go test runs
3. Edit(handler2.go)  
   → go test runs
4. Done
```

### Pattern 2: Final Commit
```
User: "Refactor database layer, commit when done"

Claude:
1. QueueOnComplete("git commit -am 'Refactor database layer'")
2. Edit(db.go)
3. Edit(db_test.go)
4. Bash("go test ./database")
5. Done
   → git commit runs
```

### Pattern 3: Both Queues
```
User: "Add feature X, test after each file, commit at the end"

Claude:
1. QueueImmediate("go test ./...")
2. QueueOnComplete("git commit -am 'Add feature X'")
3. Write(feature.go)
   → tests run
4. Write(feature_test.go)
   → tests run
5. Done
   → commit runs
```

### Pattern 4: Clearing the Queue
```
User: "Actually, stop running tests after every change"

Claude:
1. QueueImmediate("")  # Empty string clears the queue
2. "Queue cleared, tests will no longer run automatically"
```

## Testing

### Manual Test

1. Start the server and agent
2. Send a message:
   ```
   "Create a hello.txt file, and after each change run 'cat hello.txt'. 
   When done, echo 'All finished!'"
   ```

3. Observe events:
   ```
   tool_use: QueueImmediate
   queue_immediate: cat hello.txt
   
   tool_use: Write (hello.txt)
   queued_task_result: [immediate queue] cat hello.txt\nHello!
   
   tool_use: Edit (hello.txt)
   queued_task_result: [immediate queue] cat hello.txt\nHello World!
   
   done (about to emit)
   queued_task_result: [completion queue] echo 'All finished!'\nAll finished!
   done
   ```

### Integration Test Scenarios

1. **Basic immediate queue**
   - Queue a command
   - Execute a tool
   - Verify command ran

2. **Basic completion queue**
   - Queue a command
   - Execute tools
   - Verify command ran after done

3. **Clearing immediate queue**
   - Queue a command
   - Clear with empty string
   - Execute tool
   - Verify command didn't run

4. **Multiple commands**
   - Queue multiple immediate commands
   - Execute tool
   - Verify all ran in order

5. **Error handling**
   - Queue failing command
   - Verify error event emitted
   - Verify work continues

## Future Enhancements

### Possible additions:
1. **Conditional execution**: Only run if previous command succeeded
2. **Queue inspection**: Tool to list queued commands
3. **Queue persistence**: Survive agent restart
4. **Rate limiting**: Prevent abuse with many queued commands
5. **Queue priority**: Order execution differently
6. **Variable substitution**: Use session variables in commands

### System prompt additions:
Consider adding guidance to Claude about when to use queues:
```
When the user asks you to:
- "test after each change" → Use QueueImmediate with test command
- "check X after every file" → Use QueueImmediate
- "commit when done" → Use QueueOnComplete with git commit
- "deploy at the end" → Use QueueOnComplete
- "notify when finished" → Use QueueOnComplete
```

## Limitations

1. **No cancellation**: Once queued, commands will run (can clear immediate queue only)
2. **No timeout**: Queued commands run with same timeout as regular Bash tool
3. **No dependency chain**: Commands don't receive output from previous commands
4. **No conditional logic**: Can't do "only commit if tests pass"
5. **Bash only**: Currently only supports Bash commands, not other tools

## Design Rationale

### Why tools instead of message metadata?
- Claude can make intelligent decisions about when to queue
- Visible in conversation history
- User doesn't need to learn special syntax
- Flexible: Claude can queue different commands based on context

### Why not modify the conversation loop?
- Separation of concerns: Loop handles LLM interaction, Worker handles queues
- Easier to test and maintain
- Event-based: Natural fit with existing pub/sub architecture
- No breaking changes to loop API

### Why intercept at the emit level?
- Single point of control
- All events flow through emit
- Easy to add new queue types
- Clean separation from business logic

### Why store in Hub instead of Worker?
- Hub is shared state by design
- Thread-safe queue management
- Could support external queue inspection later
- Consistent with message queue pattern
