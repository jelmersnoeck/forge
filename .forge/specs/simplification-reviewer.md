---
id: simplification-reviewer
status: implemented
---
# Add simplification reviewer and sharpen maintainability reviewer

## Description
Add a new "simplification" review agent that focuses on code simplicity,
readability, and reducing unnecessary complexity. Additionally, refocus the
existing maintainability reviewer to avoid overlap with code-quality (which
already covers correctness/edge-cases) and the new simplification reviewer
(which takes over readability/complexity concerns). The maintainability reviewer
should narrow to structural/architectural health: naming, DRY, dead code,
consistency, and separation of concerns — not complexity or readability per se.

## Context
- `internal/review/reviewers.go` — all reviewer types, `DefaultReviewers()`, `DefaultReviewersWithSpec()`
- `internal/review/orchestrator_test.go` — `TestReviewersList` asserts reviewer count and names
- `.forge/specs/automated-review.md` — master spec for the review system (needs Behavior update)

## Behavior
- `DefaultReviewers()` returns 5 reviewers: security, code-quality, simplification, maintainability, operational
- `DefaultReviewersWithSpec()` returns 6 (adds spec-validation)
- The new `SimplificationReviewer` struct implements `Reviewer` with `Name() == "simplification"`
- Simplification reviewer prompt focuses on:
  - Overly complex logic that can be rewritten more simply
  - Unnecessary abstractions or indirection
  - Deep nesting that could use early returns or guard clauses
  - Verbose code that has simpler idiomatic equivalents
  - Over-engineering (interfaces with one implementation, unnecessary generics, etc.)
  - Code that requires a comment to explain when a rewrite would be self-explanatory
  - Boolean logic that could be simplified
  - Unnecessary temporary variables, redundant checks, dead branches
- Severity guide for simplification:
  - critical: actively confusing code that is likely to cause bugs due to complexity
  - warning: significant simplification opportunity that hurts readability
  - suggestion: minor simplification for clarity
- Maintainability reviewer prompt is refocused to structural/architectural health:
  - Naming (variables, functions, types) — misleading or unclear
  - DRY violations (copy-pasted logic that should be extracted)
  - Dead code, unused imports, unreachable branches
  - Inconsistent patterns within the codebase
  - Poor separation of concerns, god functions/types
  - Magic numbers or strings that should be constants
  - Missing or misleading documentation on exported symbols
  - Removes overlap with simplification: no more "complexity" or "readability" focus areas
  - Removes overlap with code-quality: no more "overly clever code" (that's simplification now)
- Review event counts update: `/review` reports correct agent count (5 reviewers × N providers without spec, 6 × N with spec)

## Constraints
- Must not change the `Reviewer` interface — only add a new struct implementing it
- Must not change orchestrator, consolidation, or any other review infrastructure
- Simplification reviewer gets the same inputs as all other reviewers (diff only, no tools)
- No new dependencies
- Existing reviewer output format (JSON array of findings) must not change

## Interfaces

```go
// internal/review/reviewers.go

type SimplificationReviewer struct{}

func (SimplificationReviewer) Name() string       // returns "simplification"
func (SimplificationReviewer) SystemPrompt() string

// Updated:
func DefaultReviewers() []Reviewer     // returns 5 reviewers (was 4)
func DefaultReviewersWithSpec() []Reviewer // returns 6 reviewers (was 5)
```

## Edge Cases
- Simplification and maintainability findings may still overlap on edge cases (e.g., a "magic number" that also makes logic hard to follow) — the consolidation step handles dedup, so this is acceptable
- Empty diff → all reviewers (including simplification) are skipped by the orchestrator; no change needed
- Simplification reviewer returns empty array for well-written code — same behavior as all other reviewers
- Test `TestReviewersList` must be updated: assert 5 defaults (was 4), 6 with spec (was 5), new name "simplification" present in the list
