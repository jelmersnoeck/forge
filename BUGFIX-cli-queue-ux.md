# CLI Queue UX Bugfixes

## Issues Fixed

### 1. Input Not Clearing After Sending Message
**Problem**: When hitting Enter to send a message, the input field wasn't being cleared, leaving the old text visible.

**Solution**: Move `m.input = ""` to execute immediately after capturing the text, before any queue logic (line 137).

### 2. First Message Shows as "Queued"
**Problem**: Even when there's no work in progress, the first message was being added to the queue and displayed with the "→" indicator, making it look like it's waiting rather than executing.

**Solution**: Changed the queue logic to:
1. Check if the queue is empty FIRST
2. If empty, add to queue and send immediately (so it shows as "in progress" with the → indicator)
3. If not empty, just add to queue (will be sent when current work completes)

This way, the first message shows as "→ Current message (processing)" immediately, and subsequent messages show as "  Next message (waiting)".

### 3. No Cursor Indicator in Input Field
**Problem**: There was no visual cursor in the input field, making it hard to see where you're typing.

**Solution**: Added a visual cursor `│` that:
- Always appears (even when input is empty)
- Positioned at the end of the text when typing
- Uses a bright color (cyan/12) to stand out
- Format when empty: `> │ Type your message...`
- Format when typing: `> your text│`

## Code Changes

Location: `cmd/cli/main.go`

### Enter Key Handler (lines 132-147)
```go
case tea.KeyEnter:
    if m.input == "" {
        return m, nil
    }
    text := m.input
    m.input = "" // Clear input immediately
    
    // If nothing in queue, send immediately without queuing
    if len(m.queue) == 0 {
        m.queue = append(m.queue, text)
        return m, m.sendMessage(text)
    }
    
    // Otherwise, add to queue (will be sent when current work completes)
    m.queue = append(m.queue, text)
    return m, nil
```

### Input Display with Cursor (lines 231-250)
```go
// Build input area with cursor
cursor := lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Render("│")
var inputContent string
if m.input == "" {
    inputDisplay := dimStyle.Render("Type your message...")
    inputContent = promptStyle.Render("> ") + cursor + " " + inputDisplay
} else {
    inputContent = promptStyle.Render("> ") + m.input + cursor
}
// Make input area full width (accounting for border and padding)
inputArea := inputBorderStyle.Width(m.width - 4).Render(inputContent)
```

## Testing

To test these changes:

1. Build the CLI: `just build-cli`
2. Run the server: `just dev-server` (in another terminal)
3. Run the CLI: `./forge-cli`
4. Observe:
   - Cursor is visible in the input field
   - When you type and press Enter, the input clears immediately
   - First message shows as "→ Message text (processing)" not in the queue list
   - Subsequent messages show as queued with "  " prefix
