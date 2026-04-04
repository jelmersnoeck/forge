# Forge Prompt Caching Strategy

## Overview

Forge implements Anthropic's prompt caching to minimize API costs by caching stable parts of the conversation. The Anthropic API allows **max 4 `cache_control` blocks** across the entire request (system + tools + messages).

## Current Strategy

Following claude-code's proven approach, we use all 4 cache slots efficiently:

### 1. System Blocks (2 slots)

**Block 1: Static Content** (global scope, 1h TTL)
- Base system prompt
- Environment information (working directory, platform, date)
- AGENTS.md content (project/user instructions)

This block has `scope: "global"` to share cache across sessions for the same project.

**Block 2: Dynamic Content** (session scope, 1h TTL)
- AGENTS.md learnings
- Rules from `.forge/rules/`
- Skill descriptions
- Agent definitions

This block changes when learnings are added, but caches well within a session.

### 2. Tools (1 slot)

**Last Tool Only** (1h TTL)
- Single `cache_control` marker on the last tool in the tools array
- This creates a cache breakpoint covering all tools
- Tools rarely change, so this is very effective

### 3. Messages (1 slot)

**Last Message, Last Content Block** (1h TTL)
- Single `cache_control` marker on the last content block of the last message
- This caches the entire conversation history up to that point
- Critical for long conversations - can save 20K+ tokens per turn

## Implementation

### System Blocks

See `internal/runtime/prompt/prompt.go`:
- `Assemble()` creates 2 blocks instead of the previous 3
- Static content (base+env+AGENTS.md instructions) merged into one block
- Dynamic content (learnings+rules+skills+agents) in second block

### Tools

See `internal/tools/registry.go`:
- `Schemas()` adds `cache_control` to the last tool only
- Covers all tools with a single breakpoint

### Messages

See `internal/runtime/loop/loop.go`:
- `addMessageCacheControl()` adds cache to last message before each API call
- Creates a deep copy to avoid mutating history
- Re-applies after compaction

## Cache Health Monitoring

The loop tracks cache performance:
- `checkCacheHealth()` in `internal/runtime/loop/loop.go`
- Warns when cache_read_tokens drops >5% and >2K tokens
- Helps detect unexpected cache invalidation

## Why This Matters

**Without message caching:**
- Every turn re-sends the entire conversation history
- Long conversations = 20K+ uncached input tokens per turn
- Very expensive for extended sessions

**With message caching:**
- Conversation history cached after first turn
- Subsequent turns only pay for new messages
- Can reduce input tokens by 90%+ in long conversations

## Example Cache Breakdown

For a conversation with 3 turns:

**Turn 1:**
- System (static): 2K tokens → **cache write**
- System (dynamic): 1K tokens → **cache write**
- Tools: 3K tokens → **cache write**
- Message 1: 100 tokens → **cache write**
- Total: 6.1K tokens (all cache creation)

**Turn 2:**
- System (static): 2K tokens → **cache hit** (global)
- System (dynamic): 1K tokens → **cache hit**
- Tools: 3K tokens → **cache hit**
- Message 1: 100 tokens → **cache hit**
- Message 2: 100 tokens → new
- Message 3: 150 tokens → **cache write** (new last message)
- Total input: 350 tokens (250 new + 100 cache write)
- Cache read: 6K tokens

**Turn 3:**
- System + Tools + Messages 1-2: **cache hit** (6.25K tokens)
- Message 4: 120 tokens → new
- Message 5: 130 tokens → **cache write**
- Total input: 250 tokens (250 new)
- Cache read: 6.25K tokens

## Cost Impact

Based on Anthropic pricing (as of 2024):
- Input tokens: $3 / 1M tokens
- Cache write: $3.75 / 1M tokens (25% markup)
- Cache read: $0.30 / 1M tokens (90% discount)

**Turn 1 (no cache):** 6.1K tokens × $3 = $0.0183

**Turn 2 (with cache):**
- New: 250 tokens × $3 = $0.00075
- Cache read: 6K tokens × $0.30 = $0.0018
- Total: $0.00255 (vs $0.0213 without cache = **88% savings**)

**Turn 3 (with cache):**
- New: 250 tokens × $3 = $0.00075
- Cache read: 6.25K tokens × $0.30 = $0.001875
- Total: $0.002625 (vs $0.0225 without cache = **88% savings**)

## References

- Anthropic Prompt Caching Docs: https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching
- claude-code implementation: `src/services/api/claude.ts` (addCacheBreakpoints)
- Max 4 cache_control blocks: API constraint
