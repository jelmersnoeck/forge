---
id: review-loop-hardening
status: implemented
---
# Bump review loop to 10, persist on criticals, drop praise

## Description
The review→fix cycle in the SWE orchestrator is currently capped at 2 loops.
Increase to 10, and when the cap is hit with remaining critical findings, reset
the counter so critical issues are always addressed. Remove the "praise" severity
entirely — it adds no value in a continuous review loop.

## Context
Files that change:

- `internal/agent/phase/orchestrator.go` — `maxReviewCycles` const, review loop
  logic in `runSWEPipeline`, `formatFindingsForCoder` (remove praise skip)
- `internal/agent/phase/orchestrator_test.go` — `TestNewSWEOrchestrator`,
  `TestFormatFindingsForCoder` (remove praise case)
- `internal/review/review.go` — remove `SeverityPraise` const
- `internal/review/reviewers.go` — remove `- praise: ...` line from every
  reviewer's severity guide; remove `praise` from `findingJSONFormat`
- `internal/review/orchestrator.go` — `formatProviderSummary` (drop praise from
  severity iteration), `FormatFindingsMessage` (remove praise skip),
  `HasActionableFindings` (simplify — no praise to exclude), `formatSummary`
  (drop praise from total/output)
- `internal/review/orchestrator_test.go` — update test cases that reference
  `SeverityPraise`, remove "only praise" test cases or convert them to empty findings
- `cmd/forge/cli.go` — remove `"praise"` case in finding display switch
- `.forge/specs/automated-review.md` — update severity list, interfaces, edge cases

## Behavior
- The review→fix loop runs up to 10 cycles before stopping.
- When 10 cycles are exhausted and the remaining findings include at least one
  `critical`, the counter resets and the loop continues (another 10 cycles).
- There is no upper bound on resets — critical issues are always pursued.
- The three remaining severity levels are: `critical`, `warning`, `suggestion`.
- Reviewer prompts no longer mention "praise" as a valid severity.
- `HasActionableFindings` returns true when any finding exists (no praise to
  filter out).
- `FormatFindingsMessage` formats all findings (no praise to skip).
- `formatFindingsForCoder` formats all findings (no praise to skip).
- Summary and provider-summary formatting only iterate `critical`, `warning`,
  `suggestion`.
- CLI finding display has no `"praise"` case.

## Constraints
- Must not remove the `SeverityPraise` const from compiled code if existing
  sessions in the wild could have serialized `"praise"` findings in JSONL —
  actually, these are ephemeral event data, never persisted for replay. Safe
  to remove outright.
- Must not introduce an infinite loop for non-critical findings — only critical
  triggers reset.
- The `findingJSONFormat` template shown to LLMs must reflect only the three
  valid severities.

## Interfaces
```go
// internal/agent/phase/orchestrator.go
const maxReviewCycles = 10

// internal/review/review.go
const (
    SeverityCritical   Severity = "critical"
    SeverityWarning    Severity = "warning"
    SeveritySuggestion Severity = "suggestion"
)

// internal/review/orchestrator.go — new function
func HasCriticalFindings(results []ReviewResult) bool
```

## Edge Cases
- All 10 cycles pass with only warnings/suggestions remaining → loop ends,
  warning emitted, no reset.
- Cycle 10 has one critical → counter resets, coder gets another pass, next
  review may be clean → loop exits.
- Reviewer returns only errors (no findings) → `HasCriticalFindings` returns
  false, loop ends at 10.
- LLM still emits a `"praise"` finding despite updated prompt → it will be
  parsed into a `Finding` with an unrecognized `Severity` string; it will count
  as actionable (triggering a fix cycle) but won't match `SeverityCritical`, so
  it won't trigger a counter reset. The summary total won't include it (only
  known severities are summed). Harmless — the coder sees it and moves on.
- Zero findings from all reviewers → loop exits immediately (no actionable
  findings).
