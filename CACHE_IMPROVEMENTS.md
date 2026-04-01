# Cache Control Improvements

## Problem

Anthropic's prompt caching feature significantly reduces API costs by caching frequently-reused content. However, we weren't using it optimally:

1. **Missing TTL on several system blocks** - AGENTS.md, Rules, Skills, and Agent descriptions only had `Type: "ephemeral"` without TTL, defaulting to 5 minutes instead of 1 hour
2. **No cache control on tools** - Tool schemas weren't being cached at all
3. **Unused cache control field** - ToolSchema type didn't even support CacheControl

## Changes

### 1. Added TTL to all system prompt blocks (internal/runtime/prompt/prompt.go)

All blocks now have `TTL: "1h"` since they rarely change:
- ✅ Base prompt (already had 1h)
- ✅ Environment info (already had 1h)
- ✅ CLAUDE.md (already had 1h)
- ✅ **AGENTS.md (fixed - now has 1h)**
- ✅ **Rules (fixed - now has 1h)**
- ✅ **Skills (fixed - now has 1h)**
- ✅ **Agent descriptions (fixed - now has 1h)**

### 2. Added cache control to tools (internal/tools/registry.go)

Tool schemas now include:
```go
CacheControl: &types.CacheControl{
    Type: "ephemeral",
    TTL:  "1h",
}
```

This is especially important since tool schemas are:
- Static (never change during a session)
- Verbose (each tool has detailed descriptions and JSON schemas)
- Sent with every API request

### 3. Updated Anthropic provider to use tool cache control (internal/runtime/provider/anthropic.go)

The provider now checks for `tool.CacheControl` and sets it on the SDK's ToolParam:
```go
if tool.CacheControl != nil {
    cacheControl := anthropic.NewCacheControlEphemeralParam()
    if tool.CacheControl.TTL == "1h" {
        cacheControl.TTL = anthropic.CacheControlEphemeralTTLTTL1h
    }
    toolUnion.OfTool.CacheControl = cacheControl
}
```

### 4. Added CacheControl field to ToolSchema (internal/types/types.go)

```go
type ToolSchema struct {
    Name         string        `json:"name"`
    Description  string        `json:"description"`
    InputSchema  map[string]any `json:"input_schema"`
    CacheControl *CacheControl `json:"cache_control,omitempty"`
}
```

## Expected Impact

### Cost Savings

With these changes, API requests will:
- **Cache write** on first request: Creates cache entries for system blocks + tools (higher cost)
- **Cache read** on subsequent requests: Reads from cache instead of processing tokens (90% cheaper)

For a typical session with:
- ~5K tokens of system prompt + tools
- 10 back-and-forth messages

**Before:**
- Every request processes full 5K tokens
- Total: 10 × 5K = 50K input tokens

**After:**
- First request: 5K tokens (cache write)
- Next 9 requests: 5K × 0.1 = 0.5K tokens each (cache read)
- Total: 5K + (9 × 0.5K) = 9.5K input tokens
- **Savings: 81% on input tokens**

### Cache Hits

Anthropic's cache persists for:
- 5 minutes: Default TTL (we're not using this)
- 1 hour: Extended TTL (what we're using)

Our system blocks and tools are perfect for 1h caching because:
- CLAUDE.md, AGENTS.md, Rules rarely change mid-session
- Tools never change (they're code)
- Base prompt is static
- Environment info only changes daily (date)

## Testing

Added tests to verify:
- ✅ All system blocks have cache control with 1h TTL (TestAssemble_CacheControlTTL)
- ✅ All tool schemas have cache control with 1h TTL (TestRegistry)
- ✅ Anthropic provider correctly sets cache control on tools

## Notes

- **Scope field**: We set `Scope: "global"` on CLAUDE.md but the Go SDK (v1.27.1) doesn't expose this field yet. The API supports it for cross-session caching, but we're blocked by SDK support.
- **API ordering requirement**: Anthropic requires cache control blocks to be ordered by TTL (1h before 5m) across tools → system → messages. We're using 1h everywhere so this is fine.
- **Token counting**: Anthropic counts cached tokens differently:
  - `input_tokens`: Regular processing
  - `cache_creation_tokens`: Writing to cache (first time)
  - `cache_read_tokens`: Reading from cache (10% cost)

## Future Improvements

1. **Monitor cache hit rates** - Add metrics to track how often we're hitting vs. missing cache
2. **SDK upgrade** - Watch for SDK support for `scope: "global"` to enable cross-session caching
3. **Dynamic TTL** - Consider shorter TTL for AGENTS.md if it changes frequently in practice
4. **Message history caching** - Explore caching recent message history (requires per-session strategy)
