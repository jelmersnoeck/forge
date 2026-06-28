# Learnings - 2026-06-28 16:25

- Anthropic 400 'text content blocks must be non-empty' is best fixed at the single buildRequest chokepoint (internal/runtime/provider/anthropic.go): skip text blocks where strings.TrimSpace(block.Text)=='' and drop messages left with zero content blocks. Source-side guards alone are leaky because persisted-then-resumed history can reintroduce empties.
- Loop.Resume calls Loop.Send internally, so an empty-prompt resume must still run the loop without appending a user message. Refactor Send to conditionally append the user block but always call runLoop — covers both Send and Resume empty-prompt paths.
- internal/tools package tests hang under `go test ./...` (Bash/Grep idle-watchdog + network); run targeted package tests instead of the whole tree to avoid the idle-kill.
