---
id: autonomous-attribution
status: implemented
---
# Autonomous-agent attribution on commits and PRs

## Description
Make it obvious — both to humans reading commits/PRs and to downstream tooling
— that work produced inside a Forge session was authored by a human and
co-authored by Forge acting on that human's behalf. The human stays the
**author** (their git identity, their `user.email`, their GitHub avatar on
the commit); Forge is the **co-author** — same shape Claude Code uses with
`Co-authored-by: Claude <noreply@anthropic.com>`. A Forge-specific
`Generated-by` trailer additionally ties the commit back to the session that
produced it.

The principle: Forge does not own work. It acts on behalf of a user, and
attribution must reflect that. The user is responsible for what lands.

## Context
- `internal/attribution/` — new package with hook install/remove, env var
  helpers, PR body attribution prefix, and types (CommitConfig, AttributionConfig, PRConfig)
- `internal/config/user_config.go` — extended with CommitUserConfig,
  PRUserConfig, AttributionEnabled types; new validKeys for all attribution
  config fields; coAuthor validation
- `internal/agent/worker.go` — setupAttribution/teardownAttribution lifecycle;
  buildPRAttribution for PR creation
- `internal/agent/phase/pr.go` — PRAttributionOpts threaded through EnsurePR
  → createNewPR; attribution.PrependAttribution called before ghCreatePR
- `internal/tools/bash.go` — SetBashExtraEnv + injection into cmd.Env
- `internal/attribution/attribution_test.go` — tests for all attribution functions
- `internal/config/user_config_test.go` — tests for new config keys
- Existing config persistence: `~/.forge/config.toml` (per `forge config set`)

## Behavior

### Author vs co-author — the model
- **Author** = the human on whose behalf the session is running. Comes from
  the git environment Forge inherits (`user.name` / `user.email`, or
  `GIT_AUTHOR_*` env vars when the gateway/bridge injects them). Forge does
  NOT override this.
- **Co-author** = Forge itself, as a fixed identity. Appended as a trailer
  to every commit so it's visible in `git log` and renders on GitHub.
- The session-initiator (the human) needs no special trailer — they are
  already the commit author.

### New config fields
Persistent config (settable via `forge config set <key> <value>`):

- `commit.attribution.enabled` — bool, default `true`. Master switch. When
  `false`, no autonomous-attribution trailers are added regardless of other
  settings.
- `commit.attribution.coAuthor` — string, default
  `"Forge <forge@noreply.invalid>"`. The fixed Forge identity appended as
  `Co-authored-by: <value>` on every commit. Operators can override to brand
  a self-hosted Forge instance (e.g. `"Forge (acme) <forge@acme.example>"`)
  but the default is a single canonical value, NOT a user-specific one.
- `commit.attribution.generatedBy` — string, default `"forge"`. When
  attribution is enabled, a trailer is appended:
  `Generated-by: <generatedBy> session=<session-id>`.
- `pr.attribution.enabled` — bool, default `true`. When true, the PR body
  produced by the deterministic PR-creation step is prefixed with an
  attribution block (see "PR body prefix" below).

### Commit trailer behavior
- Trailers are appended to the commit message via git's standard trailer
  mechanism (`git interpret-trailers --in-place`) so they survive amend and
  squash.
- The commit **author** is left alone — it comes from the user's git
  config / `GIT_AUTHOR_*` env. Forge never writes `--author=...`.
- Trailers are added at the END of the message, in this order:
  1. `Co-authored-by: <commit.attribution.coAuthor>`
  2. `Generated-by: <commit.attribution.generatedBy> session=<session-id>`
- Both trailers are gated by `commit.attribution.enabled`. When the master
  switch is off, neither is added.
- If a trailer of the same key+value would be added twice (e.g. amending an
  existing Forge commit), git's trailer logic dedupes — leave it to git.
- The session id used is the current Forge session id (already tracked in
  the runtime). It is a stable id for the lifetime of the session.

### PR body prefix
When `pr.attribution.enabled` is true, the deterministic PR-creation step
prepends this block to the body it would otherwise produce:

```
> 🤖 This PR was opened by a Forge session acting on behalf of @<author>.
> Session: `<session-id>`
> Co-authored by: <commit.attribution.coAuthor>

---

<original PR body>
```

`@<author>` is resolved from the GitHub login associated with the commit
author email (best-effort via `gh api /search/users`; fall back to the bare
email if no match).

### Where it plugs in
- **Commit creation** is currently driven by the agent calling the Bash tool
  (or, in some phases, deterministic git calls). For deterministic git calls,
  apply trailers via `git interpret-trailers`. For agent-driven commits via
  Bash, expose a small wrapper that the system prompt instructs the agent to
  use — or, more reliably, install a one-shot `prepare-commit-msg` git hook
  inside the worktree at session start that does the trailer injection
  transparently. **Prefer the hook approach** — it covers both deterministic
  and agent-driven commits without changing the prompt.
- **PR body** is generated by the existing Haiku call in the deterministic PR
  creation step. Prepend the attribution block to the body *after* the Haiku
  call returns and *before* `gh pr create` is invoked.

## Constraints
- Trailers MUST be appended, not replace existing trailers.
- The hook must not break commits when run in a worktree without Forge
  context — guard it on the presence of the `FORGE_SESSION_ID` env var (or
  similar) that Forge sets when shelling out.
- Don't write `commit.coAuthor` automatically — leave it as an explicit
  opt-in via `forge config set`. (Future: read from session-initiator
  metadata when the gateway propagates it; out of scope here.)
- Don't touch the `Signed-off-by` trailer or DCO behavior.
- Don't sign commits — signing is controlled by the user's git config, not
  by Forge.

## Interfaces

```go
// internal/attribution/attribution.go
type CommitConfig struct {
    CoAuthor    string            // "Name <email>"
    Attribution AttributionConfig
}

type AttributionConfig struct {
    Enabled     bool   // default true — master switch for all trailers
    GeneratedBy string // default "forge"
}

type PRConfig struct {
    Attribution AttributionConfig
}

func InstallCommitHook(worktreeDir string) error
func RemoveCommitHook(worktreeDir string) error
func EnvForCommit(sessionID string, cfg CommitConfig) []string
func PrependAttribution(body, sessionID, coAuthor string, enabled bool) string
```

```go
// internal/config/user_config.go
type CommitUserConfig struct {
    CoAuthor    string             `toml:"co_author"`
    Attribution AttributionEnabled `toml:"attribution"`
}

type PRUserConfig struct {
    Attribution AttributionEnabled `toml:"attribution"`
}

type AttributionEnabled struct {
    Enabled     *bool  `toml:"enabled"`      // nil = default (true)
    GeneratedBy string `toml:"generated_by"` // default "forge"
}

func (a AttributionEnabled) IsEnabled() bool // returns true if nil
```

```go
// internal/agent/phase/pr.go
type PRAttributionOpts struct {
    SessionID string
    CoAuthor  string
    Enabled   bool
}

func EnsurePR(ctx context.Context, prov types.LLMProvider, cwd, specPath string, attr PRAttributionOpts) PRResult
```

```go
// internal/tools/bash.go
func SetBashExtraEnv(envs []string)
```

## Edge Cases
- **Hook already present** in the worktree (user has their own
  `prepare-commit-msg`): back it up to `prepare-commit-msg.forge-backup` on
  install, restore on session teardown. If a backup already exists, log and
  leave the user's hook alone (do not silently overwrite).
- **Commit made outside the agent's shell** (rare — e.g. a tool that uses
  go-git directly): trailers won't be added. Document this; out of scope to
  fix.
- **`commit.coAuthor` malformed** (not "Name <email>" shape): config set
  validates and rejects with a clear error.
- **Session id not yet available** (very early in session boot before id is
  assigned): hook is installed *after* the id is known, so this shouldn't
  arise. Defensive: if `FORGE_SESSION_ID` is empty when the hook runs, skip
  all trailers silently.
- **Long commits / interactive rebase**: trailers go through
  `git interpret-trailers`, which handles all of these correctly.
- **`commit.attribution.enabled = false`**: neither trailer is added. The
  master switch gates both. If a user wants only some trailers, they set
  config values accordingly (e.g. empty `generatedBy` suppresses that line).

## Tests
- `internal/attribution/attribution_test.go`: tests for PrependAttribution
  (enabled/disabled, with/without coauthor, empty body), InstallCommitHook
  (fresh install, backup existing, backup collision error),
  RemoveCommitHook (clean remove, restore backup), EnvForCommit (all set,
  disabled, no coauthor), and two integration tests that create a real git
  repo, install the hook, make a commit, and verify trailers are present
  (or absent when env vars are empty).
- `internal/config/user_config_test.go`: tests for commit.coAuthor
  validation (valid, missing email, empty name), boolean fields
  (commit.attribution.enabled, pr.attribution.enabled), generatedBy,
  and attribution defaults (nil = true).

## Out of Scope
- Reading the human initiator's identity from gateway/session metadata —
  the bridge (Discord ↔ Forge) doesn't exist yet. When it does, it'll set
  `commit.coAuthor` per session via a config-override mechanism (separate
  spec).
- Cryptographic signing — already handled by user's git config (`gpg.format
  = ssh`, etc.); Forge stays out of the way.
- A `Reviewed-by` trailer for the review phase — possible future addition.
