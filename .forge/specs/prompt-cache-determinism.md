---
id: prompt-cache-determinism
status: implemented
---
# Deterministic system prompt assembly to keep the Anthropic prompt cache warm

## Description
The `[CACHE BREAK]` warning fired frequently because system prompt assembly was
non-deterministic: learnings, rules, and skills were iterated in load order, and
the current date was embedded in the large static (cross-session) cache block,
busting it daily. This sorts learnings/rules/skills before emission (matching the
existing agents pattern) and relocates the date out of the static block into the
dynamic block. Addresses issue #240.

## Context
- `internal/runtime/prompt/prompt.go` — `Assemble`: sorts learnings (by Path),
  Rules (by Path), SkillDescriptions (by Name); moved current date from static
  block to end of dynamic block.
- `internal/runtime/prompt/prompt_test.go` — added determinism tests.
- `internal/runtime/context/loader.go` — inspected; `os.ReadDir` already returns
  filename-sorted entries (Go 1.16+), so no change needed there. Authoritative
  sort now lives in `Assemble`.
- `internal/runtime/loop/loop.go:783` — the `[CACHE BREAK]` warning this fixes.
- `internal/tools/registry.go:72-76` — documents the same determinism rationale.

## Behavior
- Learnings emitted sorted ascending by `AgentsMDEntry.Path`.
- Rules emitted sorted ascending by `RuleEntry.Path`.
- Skills emitted sorted ascending by `SkillDescription.Name`.
- The static global block (block 0) contains working directory and platform but
  NOT the current date.
- The dynamic block (block 1) contains `Current date: YYYY-MM-DD` appended last.
- For a fixed bundle within a single day, repeated `Assemble` calls produce
  byte-identical block text (verified across 50 iterations in tests).

## Constraints
- Do not put the current date in the static/global cache block.
- Do not change `os.ReadDir` call sites in loader.go (already sorted).
- Do not exceed the existing 2 system blocks / 4 total cache_control budget.
- Keep the date inside cached content (do not strip it entirely) — it remains
  useful agent context.

## Interfaces
```go
// internal/runtime/prompt/prompt.go
func Assemble(bundle types.ContextBundle, cwd string) []types.SystemBlock

slices.SortFunc(learnings, func(a, b types.AgentsMDEntry) int { return cmp.Compare(a.Path, b.Path) })
slices.SortFunc(bundle.Rules, func(a, b types.RuleEntry) int { return cmp.Compare(a.Path, b.Path) })
slices.SortFunc(bundle.SkillDescriptions, func(a, b types.SkillDescription) int { return cmp.Compare(a.Name, b.Name) })
```

## Edge Cases
- Empty bundle: dynamic block is now always emitted because it always carries the
  date, so an empty bundle yields 2 blocks (was 1). Existing tests use
  `GreaterOrEqual(len(blocks), 1)` so this is compatible; the spec-index test still
  asserts exactly 2 blocks.
- Date placement: the date sits at the END of the dynamic block, so the dynamic
  block's prefix (learnings/rules/skills/agents/specs) stays byte-stable within a
  day; this also preserves tests that assert the dynamic block starts with
  `<system-reminder>` / `Available Skills:`.
- Day rollover mid-session: the date string changes at midnight, busting only the
  dynamic block (small) rather than the static global block (large) — a strict
  improvement over the prior behavior.
- Duplicate paths/names across user+project levels: stable comparator preserves
  relative input order for equal keys; output remains deterministic given a
  deterministic input order from the loader.
