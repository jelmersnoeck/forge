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

## Session Reflection - 2026-04-03 18:45

**Summary:** Implemented MCP client support for Forge: pure-Go JSON-RPC over Streamable HTTP transport, OAuth 2.1 with DCR/PKCE, token persistence, config loading, and tool bridge into Forge's registry. 11 new files, 24 tests passing.

**Mistakes & Improvements:**
- Initial oauth_test.go had a self-referential closure bug (mcpServer.URL used inside its own handler before assignment) - caught by compiler but wasted a round

**Successful Patterns:**
- Kept it pure standard library Go - no external MCP SDK needed
- Clean separation into config/token_store/oauth/client/bridge layers
- All tests use httptest.NewServer for real HTTP testing, no mocks
- Non-fatal MCP integration - agent works fine without MCP config
- Tool namespacing (mcp__server__tool) prevents collisions with built-in tools
- Community references throughout test data as specified

**Future Suggestions:**
- Consider adding MCP resources/prompts support later
- Device code flow would be needed for headless OAuth environments
- tools/list change notifications (listChanged) could be valuable for long-running sessions
- Could add an `mcp status` subcommand to list connected servers and their tools

