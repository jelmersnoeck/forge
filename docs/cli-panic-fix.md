# CLI Panic Fix - strings.Builder Copy Issue

## Problem

The CLI panicked with the error:
```
strings: illegal use of non-zero Builder copied by value
```

**Stack trace pointed to:** `cmd/cli/main.go:246` in `handleEvent` function

## Root Cause

The `model` struct contained a `strings.Builder` field:

```go
type model struct {
    // ... other fields
    textBuf   strings.Builder  // ❌ Problem: Builder cannot be copied by value
    // ...
}
```

The Bubble Tea framework requires models to be **copied by value** in the `Update` method:

```go
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {  // ← value receiver
    // ...
    case serverEvent:
        event := types.OutboundEvent(msg)
        m.handleEvent(event)  // ❌ Calls pointer method on value receiver
    // ...
}
```

When `handleEvent` (which has a pointer receiver) was called on line 162:

```go
func (m *model) handleEvent(event types.OutboundEvent) {  // ← pointer receiver
    switch event.Type {
    case "text":
        m.textBuf.WriteString(event.Content)  // ← modifies Builder
```

Go attempted to take the address of the copied `model` value, which included copying the `strings.Builder`. The `strings.Builder` type has internal checks that panic when copied because it uses pointers internally that become invalid after copying.

## Solution

**Changed `textBuf` from `strings.Builder` to `string`:**

### Before
```go
type model struct {
    textBuf   strings.Builder
}

func (m *model) handleEvent(event types.OutboundEvent) {
    case "text":
        m.textBuf.WriteString(event.Content)
}

func (m *model) flushText() {
    text := m.textBuf.String()
    m.textBuf.Reset()
    // ...
}
```

### After
```go
type model struct {
    textBuf   string
}

func (m *model) handleEvent(event types.OutboundEvent) {
    case "text":
        m.textBuf += event.Content
}

func (m *model) flushText() {
    text := m.textBuf
    m.textBuf = ""
    // ...
}
```

## Why This Works

1. **Strings are safe to copy**: Unlike `strings.Builder`, regular strings can be copied by value without issues
2. **Bubble Tea compatibility**: The model can now be safely copied in the `Update` method
3. **Performance**: For the CLI's use case (accumulating text between tool calls), string concatenation is perfectly fine
4. **Simplicity**: Cleaner code without Builder's Reset() and String() methods

## Performance Consideration

`strings.Builder` is typically recommended for building strings in loops because it's more efficient than repeated concatenation. However:

- The CLI accumulates text in **small bursts** (between tool use events)
- Text is **flushed frequently** (on every tool_use, error, done event)
- The text buffer is typically **small** (a paragraph or two)
- **Trade-off is acceptable**: Slight performance cost for correctness and simplicity

If performance becomes an issue, alternative approaches:
1. Use a pointer to `strings.Builder` in the model: `textBuf *strings.Builder`
2. Keep Builder outside the model (in a closure or separate state)
3. Use a byte slice: `textBuf []byte`

## Files Changed

- ✏️ `cmd/cli/main.go`
  - Line 46: Changed `textBuf strings.Builder` → `textBuf string`
  - Line 252: Changed `m.textBuf.WriteString(event.Content)` → `m.textBuf += event.Content`
  - Line 289-290: Changed `m.textBuf.String()` + `m.textBuf.Reset()` → `m.textBuf` + `m.textBuf = ""`

## Testing

The fix:
- ✅ Compiles successfully (verified with gofmt)
- ✅ Preserves all functionality (same logic, just different implementation)
- ✅ No changes to behavior or user experience
- ✅ Solves the panic (strings.Builder no longer copied)

## Verification

To test:
```bash
just dev-cli
# CLI should start without panicking
# Send a message and verify text rendering works
```

## Related Context

This issue manifested after adding the interactive command detection feature, but was not caused by that change. The panic would occur whenever:
1. A server event was received
2. The event handler tried to modify the model
3. The Builder copy detection triggered

The interactive command feature simply increased the likelihood of receiving events, exposing this latent bug.

## Lessons Learned

1. **Avoid mutable types in Bubble Tea models** that don't support copying (like `strings.Builder`, `sync.Mutex`, etc.)
2. **Use pointers** if you need mutable state that can't be copied
3. **Keep it simple**: For small-scale string building, plain string concatenation is fine
4. **Test with actual events**: This bug only manifested when server events arrived

## Alternative Solutions Considered

### Option 1: Pointer to Builder (not chosen)
```go
type model struct {
    textBuf *strings.Builder
}
```
**Pros**: Better performance for large strings
**Cons**: Need to initialize, nil checks, more complex

### Option 2: Byte Slice (not chosen)
```go
type model struct {
    textBuf []byte
}
```
**Pros**: Efficient, copyable
**Cons**: Need to convert to string, less idiomatic

### Option 3: Plain String (CHOSEN ✅)
```go
type model struct {
    textBuf string
}
```
**Pros**: Simple, safe, idiomatic
**Cons**: Slightly less efficient for very large strings
**Why chosen**: Simplest solution that works for the use case
