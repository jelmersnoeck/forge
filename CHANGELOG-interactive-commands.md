# Changelog Entry - Interactive Command Handling

## [Unreleased] - 2026-03-29

### Added
- Interactive command detection in Bash tool to prevent blocking operations
- Comprehensive error messages with non-interactive alternatives
- 25+ known interactive commands (vim, less, python REPL, npm init, etc.)
- Pattern-based detection for non-interactive flags (-y, -c, pipes, redirects)
- Enhanced timeout error messages with troubleshooting tips
- Documentation: `docs/interactive-commands.md`
- Documentation: `docs/interactive-alternatives.md`
- Test suite: `TestBashToolInteractiveCommands` with 15+ scenarios
- Test suite: `TestCheckInteractiveCommand` with 30+ cases

### Changed
- `internal/tools/bash.go`: Added `checkInteractiveCommand()` function
- `internal/tools/bash.go`: Pre-execution validation in `bashHandler()`
- `internal/tools/bash_test.go`: Added interactive command test coverage

### Fixed
- Bash tool no longer blocks on interactive commands (vim, less, REPLs, etc.)
- Conversation loop doesn't hang waiting for stdin input
- Users get immediate actionable feedback instead of timeout errors

### Technical Details

**Problem:**
Interactive commands like `vim`, `less`, and bare `python` would block the conversation loop for up to 120-600 seconds waiting for TTY input that would never arrive, preventing the agent from continuing.

**Solution:**
Pre-execution detection system that:
1. Checks for non-interactive patterns (flags, pipes, redirects) → allow
2. Checks command name against known interactive commands → block with helpful message
3. Detects bare REPL invocations → block with script suggestions
4. Special-cases docker/kubectl `-it` patterns → block with flag removal suggestions

**User Impact:**
- Immediate feedback (< 1ms) vs 120s timeout
- Clear error messages explaining the issue
- Actionable suggestions for non-interactive alternatives
- Conversation flow preserved

**Code Quality:**
- ~100 new lines of production code
- ~200 new lines of test code
- Zero breaking changes (existing commands unaffected)
- Follows project conventions (table-driven tests, etc.)

### Migration Notes

No migration needed. The change is backward compatible:
- All previously working commands continue to work
- Only commands that would timeout/fail anyway are now blocked earlier
- Error messages are more helpful than before

### Future Work

See `docs/interactive-commands.md` for planned enhancements:
- Configuration via `.forge/config.yml`
- Background job support for long-running commands
- User override option with `force: true` parameter
- Smart LLM-powered command rewriting suggestions
