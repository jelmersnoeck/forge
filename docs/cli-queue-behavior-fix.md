# CLI Queue Display Fix

## Problem

When sending a message to the CLI with no other work in progress, the message was immediately displayed as "queued" even though it was being processed right away. This was confusing UX since "queued" implies waiting.

## Root Cause

The previous logic in `cmd/cli/main.go` (lines 156-164) would:
1. Check if queue is empty
2. Add message to queue
3. Send message immediately

This meant even immediately-processed messages appeared in the queue list until the "done" event arrived.

## Solution

Changed the logic to distinguish between:
- **Immediate processing**: Queue empty AND agent not working → send without adding to queue
- **Queued processing**: Queue has items OR agent is busy → add to queue

### Changes Made

**Line 157** (previously 157-159):
```go
// Before:
if len(m.queue) == 0 {
    m.queue = append(m.queue, text)
    return m, m.sendMessage(text)
}

// After:
if len(m.queue) == 0 && !m.working {
    return m, m.sendMessage(text)
}
```

**Lines 197-201** (previously 198-203):
```go
// Before:
if event.Type == "done" && len(m.queue) > 1 {
    m.queue = m.queue[1:]
    return m, m.sendMessage(m.queue[0])
} else if event.Type == "done" && len(m.queue) == 1 {
    m.queue = m.queue[1:]
}

// After:
if event.Type == "done" && len(m.queue) > 0 {
    text := m.queue[0]
    m.queue = m.queue[1:]
    return m, m.sendMessage(text)
}
```

## Behavior

### Before
- User sends message with idle agent
- Message shows in "Queued messages:" list with arrow
- Message processes
- Queue clears on "done"

### After
- User sends message with idle agent
- Message sends immediately, no queue display
- Message processes
- No queue cleanup needed

### Queue Still Shows When
- Agent is actively working (`m.working == true`)
- Queue already has pending messages
- Multiple messages sent in rapid succession

## Testing

Build and test:
```bash
go build -o forge-cli cmd/cli/main.go
```

To verify:
1. Start forge server
2. Start CLI
3. Send a message when idle → should NOT show in queue
4. Send another message while first is processing → SHOULD show in queue
5. After first completes, queued message should process and clear

## Impact

- Better UX: "queued" only shows when actually waiting
- Cleaner visual state for single-message workflows
- Queue display still accurate for multi-message scenarios
