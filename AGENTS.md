# Agent Learnings

This file contains self-improvement learnings from agent sessions. The agent automatically reflects on each session and appends insights here.

## Session Reflection - 2024-04-01 14:30

**Summary:** Implemented AGENTS.md support and Reflect tool for self-improvement loop

**Successful Patterns:**
- Used existing CLAUDE.md loading pattern for consistency
- Function-based tool definitions match codebase conventions
- Table-driven tests provide good coverage
- Added AgentsMDEntry type to ContextBundle cleanly

**Future Suggestions:**
- Consider rate limiting Reflect tool to avoid spam
- Could add metadata like session duration, token usage
- Might want to aggregate/summarize AGENTS.md periodically to avoid bloat
- Consider exposing reflection trigger as a user command
