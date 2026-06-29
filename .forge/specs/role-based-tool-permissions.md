---
id: role-based-tool-permissions
status: implemented
---
# Enforce tool permissions at execution and add role-based permission presets

## Description
The `Allow`/`Deny` lists in `PermissionConfig` are loaded into the context bundle
but never enforced when a tool runs. Tool restriction today happens only by
*omission from the schema* (`Registry.Filtered`), which the LLM can bypass via
resumed history, hallucinated tool names, or the MCP gateway. This spec adds
execution-time enforcement (defense in depth) and a named role → permission
preset map so sub-agents and phases can request a role instead of hand-listing
tools. Granular permissions (`Bash:read-only`, glob-scoped `Write:*.md`) from
issue #256 Phase 3 are explicitly out of scope; an extension point is noted.

Implements GitHub issue #256.

## Context
- `internal/types/types.go` — `PermissionConfig` (allow/deny), `MergedSettings.Permissions`,
  `ToolDefinition`. Add `AgentRolePermissions` preset map and a role resolver here
  (types package has no deps and is already imported everywhere).
- `internal/tools/registry.go` — `Registry.Execute` (line ~106) and `Registry.Filtered`
  (line ~155). Add an enforced allow/deny check inside `Execute`; add a constructor
  that stamps permissions onto a filtered registry.
- `internal/agent/worker.go` — `makeAgentRunner` (line ~1045) builds the sub-agent
  registry. Now delegates to new helper `resolveSubAgentRegistry(parent, agent)`.
- `internal/tools/agent.go` — `AgentTool` schema + `handleAgent`. `agent.Type` is
  the freeform role identifier already passed through to the sub-agent.
- `internal/runtime/task/manager.go` — `SubAgent` carries `Type`, `Tools`,
  `DisallowedTools`; no change to signatures expected.
- `internal/agent/phase/orchestrator.go`, `debate.go` — already call `Filtered`;
  must continue to work unchanged (filtering stays the primary mechanism).
- `internal/runtime/context/loader.go` (line ~405) — merges settings permissions
  into the bundle; source of the allow/deny lists that must now be enforced.

## Behavior
1. `Registry.Execute(name, ...)` rejects a tool that violates the registry's
   permission policy BEFORE invoking the handler, returning a `ToolResult` with
   `IsError: true` and text `Tool '<name>' denied by permission policy` (no Go
   `error` returned — surfaces to the model as a tool_result, not a loop abort).
2. A registry created without an explicit permission policy enforces nothing
   (all registered tools runnable) — preserves current behavior for the main loop.
3. `Registry.Filtered(allow, deny)` continues to omit tools from `All()`/`Schemas()`
   exactly as today (schema-level hiding is unchanged and remains primary).
4. New `Registry.WithPermissions(allow, deny []string) *Registry` returns a registry
   that BOTH omits disallowed tools from schemas AND enforces the policy in `Execute`.
   `"*"` in the allow list means "all tools allowed". Deny always wins over allow.
5. New exported `types.AgentRolePermissions map[string]types.PermissionConfig` with
   presets for `reviewer`, `explorer`, `coder`, `planner` (values per Interfaces).
6. New `types.ResolveRolePermissions(role string) (PermissionConfig, bool)` returns
   the preset for a known role (case-insensitive) and `false` for an unknown role.
7. In `makeAgentRunner`, when `agent.Type` matches a known role AND the caller
   passed no explicit `Tools`/`DisallowedTools`, the role preset's allow/deny are
   used. Explicit `agent.Tools`/`agent.DisallowedTools` always override the preset.
   The resolved policy is applied via `WithPermissions` so it is enforced, not just
   schema-hidden.
8. A denied tool call emits no panic and does not increment cost beyond the single
   rejected turn; the audit logger still records the call with the denial error.

## Constraints
- Must NOT return a non-nil Go `error` from `Execute` on denial — that would abort
  the conversation loop; denial must round-trip as an error `ToolResult`.
- Must NOT change `Filtered`'s existing signature or behavior; existing
  `registry_test.go::TestFiltered` cases must still pass unchanged.
- Must NOT enforce permissions on the main (top-level) interactive registry unless
  `bundle.Settings.Permissions` is set — no silent new restrictions for existing users.
- Must NOT implement granular/glob permissions (`Bash:read-only`, `Write:*.md`).
  Leave a single-line comment marking the extension point in `Execute`.
- Deny entry overrides an allow entry for the same tool, always.
- `"*"` is only meaningful in the allow list; a `"*"` in deny denies nothing special
  (treat as a literal tool name that won't match).
- Role lookup is case-insensitive; unknown roles fall back to existing freeform
  `Tools`/`DisallowedTools` behavior (no error).

## Interfaces
```go
// internal/types/types.go

// AgentRolePermissions maps a sub-agent role to its tool allow/deny preset.
// "*" in Allow means all tools permitted.
var AgentRolePermissions = map[string]PermissionConfig{
    "reviewer": {Allow: []string{"Read", "Glob", "Grep", "WebSearch"},
                 Deny: []string{"Write", "Edit", "Bash"}},
    "explorer": {Allow: []string{"Read", "Glob", "Grep"},
                 Deny: []string{"Write", "Edit", "Bash", "Agent"}},
    "coder":    {Allow: []string{"*"}, Deny: nil},
    "planner":  {Allow: []string{"Read", "Glob", "Grep", "WebSearch", "Bash"},
                 Deny: []string{"Write", "Edit"}},
}

// ResolveRolePermissions returns the preset for a role (case-insensitive).
func ResolveRolePermissions(role string) (PermissionConfig, bool)
```

```go
// internal/tools/registry.go

// WithPermissions returns a registry that omits disallowed tools from schemas
// AND enforces the allow/deny policy inside Execute. "*" in allow = all tools.
func (r *Registry) WithPermissions(allow, deny []string) *Registry

// permitted reports whether name may execute under this registry's policy.
// nil policy => everything permitted. (internal helper)
func (r *Registry) permitted(name string) bool
```

```go
// internal/agent/worker.go

// resolveSubAgentRegistry builds a sub-agent's registry: explicit Tools/
// DisallowedTools win (schema-filtered); else a known agent.Type role preset is
// applied AND enforced via WithPermissions; else an unrestricted filtered registry.
func resolveSubAgentRegistry(parent *tools.Registry, agent *types.SubAgent) *tools.Registry
```

Implemented `planner` preset (issue listed only reviewer/explorer/coder; planner
added per the issue's "Planners: Read + maybe limited bash" note — allows Bash for
inspection, denies Write/Edit).

## Edge Cases
- **Denied tool invoked anyway (resumed history / hallucination):** `Execute`
  returns `IsError: true` ToolResult `Tool 'Write' denied by permission policy`;
  loop continues, model can adapt. No Go error, no panic.
- **Empty allow + empty deny (no policy):** every registered tool runs; identical
  to today's main-loop behavior.
- **Allow contains `"*"`:** all tools permitted except any listed in deny
  (`coder` role = full access, deny empty = true yolo).
- **Unknown role string passed as `agent.Type`:** `ResolveRolePermissions` returns
  `false`; runner falls back to freeform `agent.Tools`/`agent.DisallowedTools`
  (existing behavior, no error).
- **Role preset AND explicit tools both supplied:** explicit `agent.Tools`/
  `agent.DisallowedTools` win; preset ignored (caller intent is more specific).
- **Tool not registered at all:** `Execute` still returns the existing
  `tool not found: <name>` Go error — unchanged; permission check runs only for
  registered tools.
- **Case-mismatched role (`"Reviewer"`):** resolves to the `reviewer` preset.
- **MCP gateway tool (`UseMCPTool`) under a read-only role:** if `UseMCPTool` is in
  the deny list (or not in a restrictive allow list), it is both hidden and
  enforced-denied; reviewers cannot reach MCP tools.

## Alternatives
- Enforce inside `loop.executeSingleTool` instead of `Registry.Execute`: rejected —
  duplicates logic across the loop, phase runners, and sub-agent runner; the
  registry is the single chokepoint every caller already routes through.
- Store role on `ToolContext` and check per-tool: rejected — spreads policy across
  every handler instead of one gate.
