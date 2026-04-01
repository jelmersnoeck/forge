# What Makes Claude Code's Caching So Good

Deep dive into the clever optimizations that make claude-code's prompt caching highly effective.

## 1. Two-Phase Cache Break Detection

**The Problem:** You need to know if the cache broke AND why it broke.

**Claude Code's Solution:**
```typescript
// Phase 1 (pre-call): Record state and detect changes
recordPromptState({
  system, toolSchemas, model, querySource, 
  fastMode, betas, effortValue, extraBodyParams
})

// Phase 2 (post-call): Check actual cache tokens and correlate with changes
checkResponseForCacheBreak(querySource, cacheReadTokens, messages)
```

**Why it's smart:**
- Separates "what changed" from "did cache actually break"
- Handles cases where content changes but cache still hits (global scope)
- Correlates time gaps with TTL expiry (5min vs 1h)
- Distinguishes client-side changes from server-side routing

**Real output:**
```
[PROMPT CACHE BREAK] tool schemas changed (+2/-0 tools)
[source=repl_main_thread, call #15, cache read: 45123 → 0, creation: 48901]
```

## 2. Comprehensive State Tracking

**Tracked State:**
- System prompt hash (content only, no cache_control)
- Cache control hash (scope, TTL - catches config flips)
- Tool schemas hash (all tools together)
- Per-tool hashes (identifies which specific tool changed)
- Model string
- Fast mode flag
- Global cache strategy
- Beta headers list
- Auto mode state
- Overage state
- Cached microcompact state
- Effort configuration
- Extra body params hash

**Why it's comprehensive:**
Every single thing that goes into the API request is tracked. No mystery cache breaks.

## 3. Session-Stable State Latching

**The Problem:** User goes into overage mid-session → cache TTL changes → entire cache invalidates.

**Claude Code's Solution:**
```typescript
// Bootstrap state - set once, never changed during session
let cache1hEligible = getPromptCache1hEligible()
if (cache1hEligible === null) {
  cache1hEligible = isClaudeAISubscriber() && !currentLimits.isUsingOverage
  setPromptCache1hEligible(cache1hEligible) // LATCH IT
}
```

**Latched States:**
- Cache 1h eligibility
- Cache 1h allowlist (query sources)
- Fast mode header
- AFK mode header
- Cache editing header
- Thinking clear mode

**Why it matters:** Without latching, external state changes (billing, feature flags, growthbook updates) would randomly invalidate cache mid-session. Costs would spike unpredictably.

## 4. Smart Cache Scope Selection

**The Problem:** When should you use global scope vs session scope?

**Claude Code's Strategy:**
```typescript
function shouldUseGlobalCacheScope(hasToolSchemaChanges: boolean): boolean {
  if (mcpToolCount > 0) {
    return false // MCP = user-specific, can't share
  }
  if (hasToolSchemaChanges) {
    return false // Schema instability = bad cache sharing
  }
  return true // Safe to share across sessions
}
```

**Scope Rules:**
- **Global scope:** Base system prompt, stable tool set, no MCP
- **Session scope:** MCP tools present, dynamic tool schemas, project-specific

**Why it's smart:** Global scope shares cache across users/sessions but only when safe. Wrong scope = cache pollution or breaks.

## 5. Per-Tool Hash Granularity

**The Problem:** Tool schema hash changes, but which tool? 77% of tool breaks have same tool count.

**Claude Code's Solution:**
```typescript
const perToolHashes: Record<string, number> = {}
for (let i = 0; i < tools.length; i++) {
  perToolHashes[tools[i].name] = computeHash(tools[i])
}

// On break, diff the hashes
const changedToolSchemas = toolNames.filter(name => 
  newHashes[name] !== prev.perToolHashes[name]
)
```

**Output:**
```
[CACHE BREAK] tools changed (+0/-0 tools)
changedToolSchemas: AgentTool,SkillTool
```

**Why it matters:** Tells you EXACTLY which tool's description/schema changed. AgentTool/SkillTool have dynamic content that changes frequently.

## 6. TTL-Aware Break Attribution

**The Problem:** Cache broke but nothing changed client-side. User thinks it's a bug.

**Claude Code's Logic:**
```typescript
if (parts.length > 0) {
  reason = parts.join(', ')
} else if (lastCallOver1hAgo) {
  reason = 'possible 1h TTL expiry (prompt unchanged)'
} else if (lastCallOver5minAgo) {
  reason = 'possible 5min TTL expiry (prompt unchanged)'
} else {
  reason = 'likely server-side (prompt unchanged, <5min gap)'
}
```

**Why it's helpful:** 
- Sets user expectations (TTL expiry is normal, not a bug)
- Differentiates known causes from server routing issues
- Guides debugging (client change vs server behavior)

## 7. Cache Deletion Handling

**The Problem:** Microcompact sends cache_edits deletions → cache_read_tokens legitimately drops → false positive cache break.

**Claude Code's Solution:**
```typescript
// When sending cache deletions
notifyCacheDeletion(querySource, agentId)

// In cache break detection
if (state.cacheDeletionsPending) {
  state.cacheDeletionsPending = false
  logForDebugging('cache deletion applied (expected drop)')
  return // Don't flag as break
}
```

**Why it's needed:** API-side microcompaction intentionally reduces cached prefix. Without this flag, you'd get false alarms on every compaction.

## 8. Compaction Baseline Reset

**The Problem:** After compaction, message count drops → token estimate is wrong → next call looks like huge token increase.

**Claude Code's Solution:**
```typescript
export function notifyCompaction(querySource: QuerySource, agentId?: AgentId) {
  const state = previousStateBySource.get(key)
  if (state) {
    state.prevCacheReadTokens = null  // Reset baseline
  }
}
```

**Why it matters:** Compaction is a discontinuity. Reset tracking so post-compact calls don't compare to pre-compact baselines.

## 9. Source-Specific Tracking with LRU Eviction

**The Problem:** Tracking cache state for unlimited sources = memory leak.

**Claude Code's Solution:**
```typescript
const MAX_TRACKED_SOURCES = 10

function getTrackingKey(querySource: QuerySource, agentId?: AgentId): string | null {
  // Only track main thread, SDK, and named agents
  if (querySource === 'compact') return 'repl_main_thread'
  for (const prefix of TRACKED_SOURCE_PREFIXES) {
    if (querySource.startsWith(prefix)) return agentId || querySource
  }
  return null // Don't track short-lived sources
}

// LRU eviction on insert
while (previousStateBySource.size >= MAX_TRACKED_SOURCES) {
  const oldest = previousStateBySource.keys().next().value
  previousStateBySource.delete(oldest)
}
```

**Why it's smart:**
- Tracks persistent sessions (main thread, SDK, named agents)
- Ignores ephemeral sources (speculation, prompt_suggestion, session_memory)
- Caps memory at 10 entries × ~300KB = ~3MB max
- Compact shares state with main thread (same server cache)

## 10. Lazy Diffable Content Generation

**The Problem:** Building full system prompt + tool schema strings is expensive (300KB+).

**Claude Code's Solution:**
```typescript
// Don't generate until needed
const buildDiffableContent = () => 
  buildDiffableContent(system, toolSchemas, model)

prev.buildDiffableContent = buildDiffableContent

// Only call when cache actually breaks
if (cacheBreak && changes?.buildPrevDiffableContent) {
  const diffPath = await writeCacheBreakDiff(
    changes.buildPrevDiffableContent(),  // Now generate it
    state.buildDiffableContent()
  )
}
```

**Why it matters:** 
- Most calls don't break cache → no need to generate
- Saves CPU and memory on the common path
- Only pays cost when debugging is needed

## 11. Diff File Generation

**The Problem:** Cache broke, need to see exactly what changed.

**Claude Code's Solution:**
```typescript
async function writeCacheBreakDiff(prevContent: string, newContent: string) {
  const patch = createPatch('prompt-state', prevContent, newContent)
  await writeFile(`/tmp/cache-break-${random}.diff`, patch)
  return diffPath
}
```

**Output:**
```diff
=== System Prompt ===

- You have access to 15 tools
+ You have access to 17 tools

=== Tools (17) ===

+ NewTool
+   description: A new tool
+   input_schema: {...}
```

**Why it's useful:** Visual diff shows exactly what changed. Much easier than comparing hashes.

## 12. Analytics Event Structure

**The Problem:** Need to analyze cache break patterns across all users.

**Claude Code's Solution:**
```typescript
logEvent('tengu_prompt_cache_break', {
  systemPromptChanged: boolean,
  toolSchemasChanged: boolean,
  addedToolCount: number,
  removedToolCount: number,
  changedToolSchemas: string[],
  systemCharDelta: number,
  callNumber: number,
  prevCacheReadTokens: number,
  cacheReadTokens: number,
  timeSinceLastCall: number,
  lastCallOver5minAgo: boolean,
  lastCallOver1hAgo: boolean,
  // ... 20+ fields
})
```

**Why it's comprehensive:** Can slice data by any dimension in BigQuery:
- Which tools cause most breaks?
- Is it TTL expiry or content changes?
- Which models have best cache hit rates?
- What % of breaks are server-side?

## 13. Minimum Break Threshold

**The Problem:** Token counts vary slightly between calls due to rounding/compression.

**Claude Code's Solution:**
```typescript
const MIN_CACHE_MISS_TOKENS = 2_000

if (cacheReadTokens >= prevCacheRead * 0.95 || 
    tokenDrop < MIN_CACHE_MISS_TOKENS) {
  return // Not a real break, just noise
}
```

**Why it matters:** Prevents false alarms on insignificant variation. 2K tokens ≈ $0.006, not worth alerting.

## 14. Model-Specific Behavior

**The Problem:** Haiku has different caching behavior than Sonnet/Opus.

**Claude Code's Solution:**
```typescript
function isExcludedModel(model: string): boolean {
  return model.includes('haiku')
}

if (isExcludedModel(state.model)) return
```

**Why it's needed:** Different models have different cache implementations. Haiku's behavior doesn't match the tracking assumptions, so exclude it rather than generate confusing alerts.

## 15. Hash Function Choice

**The Problem:** Need fast, stable hashing for ~300KB strings.

**Claude Code's Solution:**
```typescript
function computeHash(data: unknown): number {
  const str = jsonStringify(data)
  if (typeof Bun !== 'undefined') {
    return Number(Bun.hash(str) & 0xffffffffn)  // Fast native hash
  }
  return djb2Hash(str)  // Fallback for Node
}
```

**Why it's smart:**
- Uses platform-native hashing when available (Bun)
- Falls back to simple djb2 for compatibility
- Consistent across runs (deterministic JSON serialization)
- Fast enough for hot path

## Key Principles

1. **Track everything** - Every cache-affecting parameter
2. **Latch critical state** - Prevent mid-session breaks
3. **Differentiate causes** - Client change vs server behavior vs TTL
4. **Be granular** - Per-tool tracking, not just aggregate
5. **Handle edge cases** - Deletions, compaction, parallel tools
6. **Optimize lazily** - Don't generate diffs unless needed
7. **Cap memory** - LRU eviction on tracking state
8. **Provide context** - Diffs and analytics, not just "cache broke"

## What to Copy for Forge

**Must-have (immediate value):**
- Two-phase detection (pre-call state, post-call correlation)
- State latching (cache 1h eligibility, model, scope)
- TTL-aware attribution (distinguish expiry from changes)
- Minimum break threshold (avoid noise)

**Nice-to-have (polish):**
- Per-tool hash tracking
- Diff file generation
- Compaction baseline reset
- Model-specific exclusions

**Future (analytics):**
- Comprehensive event logging
- Source-specific tracking with LRU
- Cost tracking and cache hit rates

## The Big Idea

Claude Code's cache tracking is **defensive** and **comprehensive**:
- Defensive: Assumes cache will break, tracks WHY
- Comprehensive: Every cache-affecting parameter is monitored
- Actionable: Provides specific guidance on what changed

This is NOT over-engineering. Cache breaks are expensive and mysterious. This system turns "WTF happened?" into "AgentTool schema changed at 2:47pm".

The implementation is complex, but the value is clear: predictable token costs and fast debugging when things break.
