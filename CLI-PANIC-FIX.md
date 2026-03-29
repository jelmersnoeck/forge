# Summary: CLI Panic Fix

## Issue
CLI crashed with: `strings: illegal use of non-zero Builder copied by value`

## Root Cause
- `model` struct had `textBuf strings.Builder` field
- Bubble Tea copies models by value in `Update()` method
- `strings.Builder` panics when copied by value (has internal pointer checks)
- Calling `m.handleEvent()` (pointer receiver) on value receiver triggered the copy

## Fix
Changed `textBuf` from `strings.Builder` to `string`:

| Location | Before | After |
|----------|--------|-------|
| Line 46 (struct) | `textBuf strings.Builder` | `textBuf string` |
| Line 252 (append) | `m.textBuf.WriteString(event.Content)` | `m.textBuf += event.Content` |
| Line 289-290 (read/reset) | `text := m.textBuf.String()`<br>`m.textBuf.Reset()` | `text := m.textBuf`<br>`m.textBuf = ""` |

## Why It Works
- Plain strings are safe to copy by value
- Performance trade-off acceptable (small, frequently-flushed text buffers)
- Simpler code, no nil checks or initialization needed

## Status
✅ Fixed - gofmt validated, ready to test

## Test
```bash
just dev-cli  # Should start without panic
```

See `docs/cli-panic-fix.md` for detailed explanation.
