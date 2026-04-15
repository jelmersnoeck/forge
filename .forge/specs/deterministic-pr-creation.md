---
id: deterministic-pr-creation
status: draft
---
# Move PR creation from LLM tool to deterministic orchestrator step

## Description
PR creation is currently an LLM tool (`PRCreate`) that the agent decides to
invoke. The finalize step in the SWE orchestrator prompts the LLM to call it.
This is wasteful and unreliable — PR creation is a mechanical step, not a
creative one. Move it to deterministic Go code in the orchestrator, using a
lightweight LLM call only for generating the PR title and description.

## Context
- `internal/agent/phase/orchestrator.go` — `runFinalize` currently spawns a full
  coder loop just to call PRCreate; replace with deterministic code
- `internal/agent/phase/prompts.go` — coder prompt step 7 tells LLM to create PR;
  remove that instruction
- `internal/tools/pr_create.go` — contains git helpers (`GitOutput`, `GitOutputFull`,
  `DetectDefaultBranch`, etc.) and the tool handler; remove the tool, keep/export
  the git helpers
- `internal/tools/pr_create_test.go` — tool handler tests; replace with tests
  for the new orchestrator-level PR creation
- `internal/tools/registry.go` — `NewDefaultRegistry()` registers `PRCreateTool()`;
  remove that registration
- `internal/agent/phase/phase.go` — `PRCreate` appears in DisallowedTools for
  spec-creator, QA, reviewer phases; clean up
- `internal/agent/phase/phase_test.go` — tests reference `PRCreate` in
  DisallowedTools; update
- `internal/agent/phase/orchestrator_test.go` — tests for `buildFinalizePrompt`,
  `shouldCreatePR`; update for new deterministic flow
- `cmd/forge/cli.go` — tracks `prURL`; now receives it via `pr_url` event from
  orchestrator instead of sniffing tool output
- `internal/types/types.go` — `pr_url` event type already exists

## Behavior
- After the review cycle completes in `runSWEPipeline`, the orchestrator runs
  `runFinalize` as deterministic Go code (no LLM loop).
- `runFinalize` performs these steps in order:
  1. Check preconditions (`shouldCreatePR` — existing logic).
  2. Fetch `origin/<base>` and rebase onto it (abort + error on conflict).
  3. Push with `--force-with-lease`.
  4. Compute diff stat and commit log.
  5. Call the LLM provider with a focused prompt to generate a PR title and
     description from the diff, commit log, and spec content. Use Haiku (cheap,
     fast model) — same pattern as session title generation.
  6. Validate the generated title/description (reuse existing validators).
  7. Call `gh pr create --draft` with the generated title/description.
  8. Emit a `pr_url` event with the PR URL.
- The `PRCreate` tool is removed from the tool registry — the LLM can no longer
  call it and no prompt instructs it to.
- The coder phase prompt no longer mentions PR creation (step 7 removed).
- `DisallowedTools` lists in phases no longer reference `PRCreate`.
- Failures in `runFinalize` are non-fatal (logged, pipeline continues) — same
  as today.

## Constraints
- Do not remove the git helper functions (`GitOutput`, `GitOutputFull`,
  `DetectDefaultBranch`, `RunGitCmd`, `GHOutput`) — they are useful utilities.
  Move them if needed, but keep them exported.
- The LLM call for title/description must use a cheap model (Haiku-class), not
  the main conversation model.
- Do not fail the pipeline if `gh` is not installed, not authenticated, or PR
  creation fails for any reason.
- Do not create a PR if there are no changes or we're on main/master — same
  preconditions as today.
- Keep the existing validation logic (title length, generic titles, description
  length, commit-list detection) but apply it as a sanity check on LLM output,
  retrying once if validation fails.

## Interfaces
```go
// internal/agent/phase/orchestrator.go

// PRResult holds the output of deterministic PR creation.
type PRResult struct {
    URL      string // GitHub PR URL (empty on failure)
    Title    string // generated title
    Body     string // generated description
    Error    error  // nil on success
}

// runFinalize replaces the LLM-based finalize step with deterministic code.
// It: checks preconditions → fetch/rebase/push → LLM-generate title+body → gh pr create.
func (o *Orchestrator) runFinalize(ctx context.Context, opts OrchestratorOpts, specPath string) error
```

```go
// internal/agent/phase/pr.go (new file, extracted PR logic)

// CreatePR performs the full PR creation workflow deterministically.
func CreatePR(ctx context.Context, prov types.LLMProvider, cwd, specPath string) PRResult

// generatePRContent uses a cheap LLM call to produce a title and description.
func generatePRContent(ctx context.Context, prov types.LLMProvider, diff, commitLog, specContent string) (title, body string, err error)
```

## Edge Cases
- `gh` not installed: `runFinalize` returns error, pipeline continues, no PR
  created. Logged as non-fatal.
- Rebase conflict: rebase aborted, error returned. Pipeline continues without PR.
- LLM generates a bad title (too short, generic): retry once with feedback.
  If still bad after retry, use a deterministic fallback title derived from the
  branch name.
- LLM call fails entirely (API error, timeout): use deterministic fallback
  title/description from spec summary + commit log.
- Spec content unreadable: generate PR content from diff + commit log only.
- PR already exists for branch: `gh pr create` fails, logged, pipeline continues.
- No ANTHROPIC_API_KEY for Haiku call: use the existing provider from opts
  (it already has auth); fall back to deterministic content if provider fails.
- Context cancelled mid-finalize: standard cancellation — abort and return.
