---
id: glamour-render-after-streaming
status: draft
---
# Render markdown through glamour only after streaming completes

## Description
Currently the CLI renders text through glamour every 100ms during streaming,
producing broken output when markdown fragments are incomplete (partial code
blocks, half-formed lists, etc.). Change this so raw text is displayed during
streaming and glamour rendering happens only once a text block is complete
(i.e., when a non-text event signals the end of the block).

## Context
- `cmd/forge/cli.go` — the entire rendering pipeline lives here:
  - `model.textBuf` — accumulates streamed text tokens
  - `model.flushText()` — renders textBuf through glamour, appends to output
  - `tickMsg` handler — calls `flushText()` every 100ms during streaming
  - `handleEvent()` — calls `flushText()` on non-text events (tool_use, done, etc.)
  - `View()` — renders `m.output` lines into terminal
  - `model.renderer` — `*glamour.TermRenderer`

## Behavior
1. During streaming (`text` events arriving), raw text is appended directly to
   `m.output` as plain lines — no glamour rendering.
2. Each tick (100ms), new text that arrived since the last tick is split by
   newlines and appended to output. Incomplete lines (no trailing newline) are
   held in a line buffer until the next tick or flush.
3. When a non-text event arrives (tool_use, done, interrupted, phase_*, etc.)
   or the turn ends, the **entire accumulated text block** is rendered through
   glamour in one shot and the raw streaming lines are **replaced** in
   `m.output` with the glamour-rendered result.
4. The spinner/working indicator still shows during streaming — `textBuf`
   being non-empty should still suppress the "working..." spinner (raw text
   already visible serves the same purpose).
5. PR URL sniffing (`extractPRURL`) still works — it runs on the full
   accumulated text at flush time.

## Constraints
- Do not add new dependencies.
- Do not change any server/agent-side code — this is purely a CLI rendering change.
- Do not break the scrollback buffer — output line count may change on flush,
  scroll offset must be adjusted so the user doesn't see a jump.
- Keep the tick rate at 100ms.
- Glamour rendering must happen exactly once per text block, not per-token or
  per-tick.

## Interfaces
```go
// model gains:
//   streamStartIdx int    // index in m.output where raw streaming lines began
//   streamBuf      string // full accumulated text for glamour rendering at flush
//
// flushText() changes:
//   1. Replaces m.output[streamStartIdx:] with glamour-rendered lines
//   2. Resets streamStartIdx and streamBuf
//
// tickMsg handler changes:
//   Instead of calling flushText(), appends raw text lines to m.output
//   and tracks them for later replacement.
```

## Edge Cases
- **Empty text block**: flushText called with no accumulated text — no-op, no
  crash.
- **Text with no trailing newline**: partial last line held in buffer, rendered
  on next tick or flush.
- **Glamour render error**: fall back to keeping the raw lines already in output
  (they're already there from streaming).
- **Scroll position during replacement**: if user scrolled up, adjust
  scrollOffset so their viewport stays stable after line count changes.
- **Very long text block**: glamour renders the entire block — should be fine
  since it already does this today; just deferred to end of block instead of
  every 100ms.
- **Multiple text blocks per turn**: each tool_use flushes the current block,
  new streaming text starts a fresh block.
- **Interrupted mid-stream**: interrupted event calls flushText, glamour
  renders whatever was accumulated.
