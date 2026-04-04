---
id: spec-driven-development
status: active
---
# Spec-driven development with structured feature specifications

## Description
Forge supports spec-driven development where every feature begins as a
structured specification. Specs act as the source of truth for implementation,
acceptance testing, and intent validation. A dedicated spec agent generates
specs from prompts; the coding agent consumes them.

## Context
- `internal/spec/spec.go` — spec parser and loader
- `internal/config/config.go` — forge config (specsDir override)
- `internal/types/types.go` — SpecDocument, SpecEntry, ContextBundle.Specs
- `internal/runtime/context/loader.go` — discovers and loads specs into bundle
- `internal/runtime/prompt/prompt.go` — injects active specs into system prompt
- `.forge/agents/spec.md` — spec agent definition
- `.forge/specs/` — default spec storage directory

## Behavior
- Specs stored as markdown with YAML frontmatter in `.forge/specs/`
- Spec directory configurable via `.forge/config.json` `specsDir` field
- Config loaded from `~/.forge/config.json` (user) and `.forge/config.json` (project)
- Project config overrides user config
- Spec agent (`spec` type) generates specs from natural language prompts
- Spec agent has Read/Grep/Glob/Bash/Write/WebSearch tools; no Edit/PRCreate
- Specs have statuses: draft, active, implemented, deprecated
- Only `active` specs appear in the system prompt
- All specs are loaded into the context bundle regardless of status
- Spec sections: Header, Description, Context, Behavior, Constraints, Interfaces, Edge Cases
- Header must be 15 words or fewer
- ID must be lowercase kebab-case, used as filename
- New specs always start as `draft`

## Constraints
- No circular imports between spec, config, and context packages
- Spec parser duplicates frontmatter parsing (not imported from context package)
- Missing spec directory is not an error — returns empty slice
- Non-markdown files in spec directory are silently ignored
- Invalid spec files are silently skipped during directory loading
- Config loading never fails on missing files — only on malformed JSON

## Interfaces
```go
// internal/config
type ForgeConfig struct {
    SpecsDir string `json:"specsDir,omitempty"`
}
func Load(cwd string) (ForgeConfig, error)

// internal/spec
func ParseSpec(path string) (types.SpecDocument, error)
func LoadSpecs(dir string) ([]types.SpecEntry, error)
func FindSpecsDir(cwd string, cfg config.ForgeConfig) string

// internal/types
type SpecDocument struct {
    ID, Status, Header, Description, Context string
    Behavior, Constraints, Interfaces, EdgeCases string
    Path string
}
type SpecEntry struct {
    Path, Content, ID, Status, Header string
}
```

## Edge Cases
- Spec file with no frontmatter — status defaults to "draft", ID is empty
- Spec file with frontmatter but no body sections — all section fields are empty strings
- Spec directory doesn't exist — LoadSpecs returns nil, nil (not error)
- Config file is malformed JSON — returns error (not silently ignored)
- Spec with duplicate ID in same directory — both loaded, last wins in any map
- Config specsDir is absolute path — used as-is, not joined with cwd
- Config specsDir is relative path — joined with cwd
- Spec file is not valid markdown — best-effort parsing, no error
- Multiple ## Description headings — first one wins (sections overwrite)
