# Token Optimization Quick Start

tl;dr for implementing token optimizations from claude-code analysis

## The Big Wins

1. **1-hour cache TTL** (currently 5min default) = 12x longer cache retention
2. **Global cache scope** = Share cache across sessions  
3. **Cache break detection** = Stop wasting tokens on unintentional invalidations
4. **Accurate token tracking** = Know when context is getting full

## Immediate Actions (Weekend Project)

### Quick Win #1: Enable 1h TTL (5 minutes)

**File:** `internal/types/types.go`
```go
type CacheControl struct {
    Type  string `json:"type"`
    TTL   string `json:"ttl,omitempty"`   // ADD THIS
    Scope string `json:"scope,omitempty"` // ADD THIS
}
```

**File:** `internal/runtime/prompt/prompt.go`
```go
// Line 71 - Update CLAUDE.md cache control
CacheControl: &types.CacheControl{
    Type:  "ephemeral",
    TTL:   "1h",       // ADD THIS - was default 5min
    Scope: "global",   // ADD THIS - share across sessions
},
```

**Impact:** Immediate 60-80% token cost reduction for multi-turn sessions

### Quick Win #2: Track Cache Tokens (10 minutes)

**File:** `internal/types/types.go`
```go
// Update TokenUsage (lines 100-106)
type TokenUsage struct {
    InputTokens         int `json:"input_tokens"`
    OutputTokens        int `json:"output_tokens"`
    CacheCreationTokens int `json:"cache_creation_input_tokens"`   // RENAME
    CacheReadTokens     int `json:"cache_read_input_tokens"`      // RENAME
}
```

**File:** `internal/runtime/provider/anthropic.go`
```go
// Line 115 - Update field names to match API
CacheCreationTokens: int(usage.CacheCreationInputTokens),
CacheReadTokens:     int(usage.CacheReadInputTokens),
```

**Impact:** See cache efficiency in real-time

### Quick Win #3: Simple Cache Break Detection (15 minutes)

**File:** `internal/runtime/loop/loop.go`
```go
type ConversationLoop struct {
    // ... existing fields ...
    lastCacheRead int  // ADD THIS
}

// ADD THIS to the message handling (after usage event)
func (l *ConversationLoop) checkCacheHealth(usage types.TokenUsage) {
    if l.lastCacheRead > 0 {
        drop := l.lastCacheRead - usage.CacheReadTokens
        if drop > 2000 && usage.CacheReadTokens < int(float64(l.lastCacheRead)*0.95) {
            log.Printf("[CACHE BREAK] %d → %d tokens (-%d)", 
                l.lastCacheRead, usage.CacheReadTokens, drop)
        }
    }
    l.lastCacheRead = usage.CacheReadTokens
}
```

**Impact:** Immediately spot cache problems

## Testing Your Changes

```bash
# Build agent
just build-agent

# Start in debug mode
FORGE_DEBUG=1 ./forge-agent --port 8080

# First message - should see cache_creation_tokens
curl -X POST http://localhost:8080/messages \
  -d '{"sessionId":"test","text":"list files in current directory"}'

# Second message - should see cache_read_tokens (not cache_creation!)
curl -X POST http://localhost:8080/messages \
  -d '{"sessionId":"test","text":"read the README file"}'

# Look for logs:
# ✓ cache_creation_tokens: ~20000 (first call)
# ✓ cache_read_tokens: ~20000 (second call)
# ✓ NO cache break warning
```

## Before/After Comparison

### Without Optimizations (Current)
```
Call 1: 50K input tokens (20K cached at 5min TTL) + 2K output
Call 2: 50K input tokens (cache miss after 6min) + 2K output  
Call 3: 50K input tokens (cache miss) + 2K output

Total: 150K input + 6K output = 156K tokens
Cost: ~$0.50
```

### With Optimizations (After Changes)
```
Call 1: 50K input tokens (20K cached at 1h TTL) + 2K output
Call 2: 7K input tokens (45K from cache!) + 2K output
Call 3: 7K input tokens (45K from cache!) + 2K output  

Total: 64K input + 6K output = 70K tokens
Cost: ~$0.15 (70% savings!)
```

## What Claude Code Does Better

They have comprehensive tracking systems:

1. **Per-tool hash tracking** - Know which specific tool schema changed
2. **Session-stable state latching** - Prevent mid-session config changes from breaking cache
3. **API-side microcompaction** - Let server clear old tool results efficiently
4. **Beta feature detection** - Smart about which cache strategies to use
5. **Cost analytics** - Track USD spend, cache hit rates, etc.

See full analysis docs for implementation details on these.

## Next Steps (After Quick Wins)

1. **Add proper token estimation** - Track context window accurately
2. **Implement full cache tracker** - Hash system/tools, detect changes
3. **Add usage metrics** - Per-session cost tracking
4. **Environment config** - Make cache TTL/scope configurable
5. **Context window warnings** - Alert when approaching limits

## Key Learnings from Claude Code

### Cache Optimization Principles

1. **Cache the stable stuff**
   - CLAUDE.md content (rarely changes)
   - Tool schemas (only change on tool updates)
   - NOT environment info (changes per session)

2. **Use appropriate TTL**
   - 5min default for quick iteration
   - 1h for production/long sessions
   - Cache breaks on TTL expiry - not free!

3. **Global scope when safe**
   - System prompt can be global (no PII)
   - Project-specific rules should NOT be global
   - Tool schemas depend on if they change

4. **Track everything**
   - Cache breaks cost money - know when they happen
   - Usage metrics show optimization opportunities
   - Hash changes to identify root causes

### What NOT to Do

❌ Cache environment-specific info (CWD, timestamps)
❌ Change cache strategy mid-session (breaks cache)
❌ Ignore cache_read_tokens field (means cache is working!)
❌ Assume cache is always working (validate with logs)

### Common Cache Break Causes

1. **System prompt changes** - Added/removed CLAUDE.md content
2. **Tool schema changes** - Updated tool descriptions or parameters
3. **Model changes** - Different models have different caches
4. **TTL expiry** - Waited too long between calls
5. **Server-side routing** - Backend redistribution (rare)

## Debug Commands

```bash
# Watch agent logs for cache metrics
tail -f /tmp/forge/sessions/agent-*.log | grep -i cache

# Check current token usage
curl -s http://localhost:8080/health | jq '.metrics.tokens'

# Monitor cache hit rate
watch -n1 'curl -s http://localhost:8080/health | jq ".metrics.cache_hit_rate"'
```

## Environment Variables

```bash
# Future configuration options
export FORGE_CACHE_1H_TTL=1              # Enable 1h TTL
export FORGE_CACHE_GLOBAL_SCOPE=1        # Enable global scope
export FORGE_TRACK_CACHE_BREAKS=1        # Log cache invalidations
export FORGE_CONTEXT_WARNING=180000      # Warn at 180K tokens
export FORGE_DEBUG=1                     # Verbose cache logging
```

## Resources

- **Full Analysis:** `docs/token-optimization-analysis.md`
- **Implementation Guide:** `docs/token-optimization-implementation.md`
- **Claude Code Source:** `../claude-code/src/services/api/`
- **Anthropic Docs:** https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching

## Questions?

Common gotchas:

**Q: Why isn't my cache working?**
A: Check logs for cache_read_tokens. If 0, either no cache exists or it broke. Look for "CACHE BREAK" warnings.

**Q: Should I use global scope for everything?**
A: No! Only for content that's truly identical across sessions (base system prompt, common rules).

**Q: How much does caching save?**
A: Typically 60-80% on input tokens for cached content. A 50K prompt costs 10K instead of 50K.

**Q: What breaks the cache?**
A: Any change to cached content (system prompt, tool schemas), model changes, TTL expiry, or server routing.

**Q: Can I cache tool results?**  
A: Not directly, but API-side microcompaction can do server-side cleanup (future feature).

## Success Metrics

After implementing, you should see:

- ✓ cache_creation_tokens on first call
- ✓ cache_read_tokens on subsequent calls  
- ✓ 60-80% reduction in input token costs
- ✓ No unexpected cache breaks
- ✓ Consistent cache hits within TTL window
- ✓ Lower $/session costs

Track these over a week and compare to baseline!
