# Documentation Auto-Update Rule

After every coding session that changes code, you MUST update project
documentation before your final handoff. This is not optional.

## What to update

### AGENTS.md
Review and update any affected sections:
- **Architecture** — new subcommands, changed binary structure
- **Repository layout** — new packages, moved files, renamed directories
- **Key files** — new important files, removed files, changed responsibilities
- **Build & run** — new just targets, changed commands
- **How it works** — changed flows, new modes
- **API endpoints** — new routes, changed signatures
- **Environment variables** — new vars, removed vars, changed defaults
- **Gotchas** — new gotchas discovered, resolved old ones
- **Conventions** — changed patterns or standards
- **Agent Learnings** (bottom section) — only if learnings were added/removed
  from the inline list (note: `.forge/learnings/` files are loaded separately)

### README.md
Review and update any affected sections:
- **Features** — new capabilities, removed features
- **Project Structure** — new directories, reorganized packages
- **Tools** table — new tools, removed tools, changed descriptions
- **API Endpoints** — new routes, changed signatures
- **Environment Variables** — new vars, removed vars
- **Architecture** diagrams — if flow changed

## How to do it

1. After all implementation commits are done, review `git diff main...HEAD --stat`
   to see what changed.
2. Read the current AGENTS.md and README.md sections relevant to your changes.
3. Edit only the sections that need updating. Do not rewrite unrelated sections.
4. Commit documentation changes as a separate commit:
   `git add AGENTS.md README.md && git commit -m "docs: update AGENTS.md and README.md"`
5. If no documentation updates are needed, explicitly state: "No doc updates
   needed — changes don't affect documented architecture, APIs, or conventions."

## When to skip

- Read-only sessions (code review, investigation, no commits)
- Sessions that only modify `.forge/specs/` or `.forge/learnings/`
- Pure test-only changes that don't affect documented behavior
