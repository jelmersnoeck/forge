---
id: fix-turncancel-error-check-order
status: implemented
---
# Fix turnCancel() before error check causes all errors to show as interrupted

## Description
`turnCancel()` is called before `turnCtx.Err()` is checked, causing every error
from the conversation loop to be misclassified as "user interrupted". The context
error must be captured before the cleanup cancel.

## Context
- `internal/agent/worker.go` lines 405-421 — turn error handling block

## Behavior
- When a turn errors and the context was NOT cancelled by an interrupt,
  the error event should be emitted with `runErr.Error()`, not "interrupted".
- When a turn errors and the context WAS cancelled by an interrupt,
  `turnInterrupted = true` and "interrupted" event should be emitted.
- `turnCancel()` must still be called for goroutine cleanup.

## Constraints
- Do not change the goroutine interrupt listener structure.
- Do not change the `emit` callback or event types.

## Interfaces
Capture `turnCtx.Err()` into a local variable before calling `turnCancel()`:
```go
wasInterrupted := turnCtx.Err() == context.Canceled
turnCancel()
```

## Edge Cases
- API error (e.g. 529 overloaded) with no interrupt: should emit `error` event, not `interrupted`.
- Interrupt arrives during turn: should emit `interrupted`, set `turnInterrupted = true`.
- No error on turn completion: no change in behavior (the `if runErr != nil` guard handles this).
