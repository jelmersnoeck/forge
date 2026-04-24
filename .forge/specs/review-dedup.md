---
id: review-dedup
status: draft
---
# LLM-based deduplication and severity calibration for review findings

## Description
After all review sub-agents complete, the orchestrator runs a "review manager"
consolidation step that uses an LLM to deduplicate findings, merge near-duplicates,
and assign a single calibrated severity per unique issue. Today, 10 agents
(5 reviewers × 2 providers) independently flag the same issue with varying
severity labels and descriptions, producing noisy, redundant output that confuses
both the user and the coder fix loop.

## Context
Files and systems that change:

- `internal/review/orchestrator.go` — `Orchestrator.Run()`: insert consolidation
  step between `wg.Wait()` and the summary/emit phase. New `consolidate()` method.
- `internal/review/consolidate.go` — new file: `Consolidate()` function, prompt
  construction, response parsing, `ConsolidatedFinding` type.
- `internal/review/review.go` — add `ConsolidatedFinding` type (or extend
  `Finding` with a `Sources` field).
- `internal/review/orchestrator_test.go` — tests for consolidation logic.
- `internal/review/consolidate_test.go` — unit tests for prompt building, JSON
  parsing, edge cases.
- `internal/agent/phase/orchestrator.go` — `runReviewerWithDiff` and
  `formatFindingsForCoder` must operate on consolidated findings.

Existing types referenced:
- `review.Finding` (review.go)
- `review.ReviewResult` (review.go)
- `review.Severity` (review.go)
- `types.LLMProvider` (types/types.go)
- `types.ChatRequest` / `types.ChatDelta` (types/types.go)

## Behavior
- After all reviewer×provider goroutines finish, the orchestrator calls a
  consolidation step before emitting per-provider summaries or the final summary.
- The consolidation step sends all raw findings to an LLM (one of the already-
  available providers, preferring Anthropic) with a system prompt that instructs
  it to:
  1. Group findings that describe the same underlying issue (same file/region,
     same root cause) regardless of which reviewer or provider produced them.
  2. For each group, produce a single `ConsolidatedFinding` with:
     - A calibrated severity (`critical`, `warning`, `suggestion`) based on the
       consensus across sources — if any source says `critical`, the manager
       should evaluate whether that's justified, not just pick the max.
     - A merged description that captures the best explanation from across sources.
     - The file/line range (most specific wins).
     - A `sources` list indicating which reviewer+provider combinations flagged it.
  3. Discard findings that the manager determines are noise/false positives when
     the majority of agents did not flag them AND they are low-severity.
- The consolidation LLM call uses the same `LLMProvider.Chat()` interface as
  review agents — no new provider machinery needed.
- The consolidated findings replace the raw findings for:
  - The `review_summary` event
  - The `FormatFindingsMessage()` output sent to the coder
  - The `HasHighSeverityFindings()` / `HasCriticalFindings()` checks
- Raw findings are still emitted as `review_finding` events in real-time (no
  change to streaming behavior — users see findings as they arrive).
- A new `review_consolidated` event is emitted after consolidation, containing
  the deduplicated findings as a JSON array.
- The consolidation step has its own timeout (2 minutes), separate from the
  per-agent timeout.
- If consolidation fails (LLM error, parse error, timeout), fall back to raw
  findings — the review must not fail because dedup broke.

## Constraints
- Must not change the streaming behavior of individual `review_finding` events —
  those still arrive in real-time as sub-agents complete.
- Must not add new provider dependencies — reuse an existing provider from the
  provider map.
- Must not increase the review timeout by more than 2 minutes in the worst case.
- The consolidation prompt must request JSON output — no free-form text parsing.
- Must not lose findings — if consolidation fails, raw findings pass through
  unmodified.
- The `FormatFindingsMessage` used by the coder fix loop must use consolidated
  findings (when available) to avoid the coder fixing the same issue 5 times.
- Must not break existing tests — all current orchestrator tests must pass.

## Interfaces

```go
// internal/review/review.go

// ConsolidatedFinding is a deduplicated finding with source attribution.
type ConsolidatedFinding struct {
    Severity    Severity `json:"severity"`
    File        string   `json:"file,omitempty"`
    StartLine   int      `json:"startLine,omitempty"`
    EndLine     int      `json:"endLine,omitempty"`
    Description string   `json:"description"`
    Sources     []Source `json:"sources"`
}

// Source identifies which reviewer+provider flagged this issue.
type Source struct {
    Reviewer string `json:"reviewer"`
    Provider string `json:"provider"`
}
```

```go
// internal/review/consolidate.go

// Consolidate deduplicates and calibrates findings from multiple agents.
// Falls back to converting raw findings 1:1 if the LLM call fails.
func Consolidate(
    ctx context.Context,
    provider types.LLMProvider,
    results []ReviewResult,
) ([]ConsolidatedFinding, error)
```

```go
// internal/review/orchestrator.go — updated Run signature stays the same,
// but returns consolidated findings in addition to raw results.

// ConsolidatedResults wraps both raw and deduplicated findings.
type ConsolidatedResults struct {
    Raw          []ReviewResult
    Consolidated []ConsolidatedFinding
}

// Orchestrator.Run now returns ConsolidatedResults instead of []ReviewResult.
func (o *Orchestrator) Run(
    ctx context.Context,
    req ReviewRequest,
    emit func(types.OutboundEvent),
) ConsolidatedResults
```

```go
// Updated helpers that operate on consolidated findings.
func FormatConsolidatedMessage(findings []ConsolidatedFinding) string
func HasConsolidatedHighSeverity(findings []ConsolidatedFinding) bool
func HasConsolidatedCritical(findings []ConsolidatedFinding) bool
```

## Edge Cases
- **Zero findings across all agents** → skip consolidation entirely, emit clean
  summary immediately.
- **Single finding from single agent** → consolidation still runs (it may
  re-classify severity), but must return at least that one finding.
- **All agents flag the same issue with different severities** (e.g., one says
  `critical`, two say `warning`, two say `suggestion`) → the manager evaluates
  and picks a calibrated severity based on the actual code concern, not just
  majority vote.
- **Consolidation LLM returns invalid JSON** → log error, fall back to raw
  findings converted 1:1 into `ConsolidatedFinding` (each with a single source).
- **Consolidation LLM times out** → same fallback as invalid JSON.
- **Provider used for consolidation is the same one that produced some findings**
  → acceptable, no conflict (it's a separate call with a different prompt).
- **Very large number of findings (50+)** → the consolidation prompt may need
  truncation. If total findings exceed a reasonable limit (e.g., 100), truncate
  to the highest-severity findings first before sending to the LLM.
- **Consolidation produces more findings than the raw input** → log a warning
  but accept the output. The LLM should not invent findings, but we can't
  enforce that structurally.
- **All providers failed** → no findings to consolidate, skip the step.
