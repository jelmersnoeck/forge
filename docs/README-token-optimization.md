# Token Optimization Documentation

This directory contains comprehensive analysis and implementation guides for optimizing token usage in forge, based on claude-code's battle-tested implementation.

## Documents Overview

### 1. **token-optimization-quickstart.md** (START HERE)
Quick reference for immediate wins. Read this first to get the 3 changes that provide 70% of the value.

**Contents:**
- 30-minute implementation guide
- Before/after comparisons
- Testing commands
- Common gotchas

**When to read:** Right now, before doing anything else.

### 2. **token-optimization-analysis.md**
Complete analysis of claude-code's token optimizations with priority recommendations.

**Contents:**
- 10 major optimization strategies
- Why each matters
- Priority ranking (high/medium/low)
- Code patterns from claude-code

**When to read:** After quick wins, before implementing advanced features.

### 3. **token-optimization-implementation.md**
Step-by-step implementation guide with full code examples.

**Contents:**
- Phase 1: Foundation (types, tracking)
- Phase 2: Smart caching (1h TTL, scope)
- Phase 3: Token estimation
- Phase 4: Advanced (context management)
- Complete Go code for each phase

**When to read:** During implementation, as reference.

### 4. **claude-code-caching-deep-dive.md**
Deep dive into what makes claude-code's caching implementation excellent.

**Contents:**
- 15 clever optimizations
- Why each technique works
- Gotchas and edge cases
- Key principles to copy

**When to read:** When you want to understand WHY, not just HOW.

## Implementation Roadmap

### Phase 1: Quick Wins (1 hour)
**Goal:** 70% cost reduction with minimal code changes

- [ ] Add TTL and Scope to CacheControl type
- [ ] Enable 1h TTL on AGENTS.md blocks
- [ ] Update TokenUsage field names to match API
- [ ] Add simple cache break detection
- [ ] Test and validate cache hits

**Expected Impact:** 60-80% token cost reduction for cached content

### Phase 2: Monitoring (2-3 hours)
**Goal:** Visibility into cache health and token usage

- [ ] Implement CacheTracker with hash computation
- [ ] Add token estimation utilities
- [ ] Track session usage metrics
- [ ] Add context window warnings
- [ ] Implement cost calculation

**Expected Impact:** Catch cache breaks early, prevent context overruns

### Phase 3: Optimization (4-5 hours)
**Goal:** Eliminate unnecessary cache breaks

- [ ] Session-stable state latching
- [ ] Per-tool hash tracking
- [ ] Compaction baseline reset
- [ ] Environment-based configuration
- [ ] Debug logging and analytics

**Expected Impact:** 90%+ cache hit rate, predictable costs

### Phase 4: Advanced (Future)
**Goal:** Cutting-edge features

- [ ] API-side context management
- [ ] Task budget support (extended thinking)
- [ ] Multi-model cost tracking
- [ ] Cache sharing strategies
- [ ] Automated optimization recommendations

**Expected Impact:** Future-proofing and bleeding-edge efficiency

## Quick Reference Commands

### Build and Test
```bash
# Build agent with changes
just build-agent

# Start in debug mode
FORGE_DEBUG=1 ./forge-agent --port 8080

# Test cache (first call - cache creation)
curl -X POST http://localhost:8080/messages \
  -d '{"sessionId":"test","text":"Hello"}'

# Test cache (second call - cache read)
curl -X POST http://localhost:8080/messages \
  -d '{"sessionId":"test","text":"List files"}'
```

### Monitor Cache Health
```bash
# Watch for cache metrics
tail -f /tmp/forge/sessions/agent-*.log | grep -i cache

# Check for cache breaks
tail -f /tmp/forge/sessions/agent-*.log | grep "CACHE BREAK"
```

### Environment Variables
```bash
export FORGE_DEBUG=1                    # Verbose logging
export FORGE_CACHE_1H_TTL=1             # Enable 1h TTL
export FORGE_CACHE_GLOBAL_SCOPE=1       # Enable global scope
export FORGE_TRACK_CACHE_BREAKS=1       # Log cache breaks
export FORGE_CONTEXT_WARNING=180000     # Context warning threshold
```

## Key Metrics to Track

After implementing optimizations, monitor these:

**Cache Efficiency:**
- Cache hit rate: Should be 70-90% after first turn
- Cache read tokens: Should be high on turns 2+
- Cache creation tokens: Should only spike on first turn or cache breaks

**Token Usage:**
- Input tokens per call: Should drop 60-80% with caching
- Context window size: Should stay under 180K tokens
- Token cost per session: Should drop 50-70% overall

**Cache Stability:**
- Cache breaks per session: Should be <2 for typical sessions
- Time between breaks: Should exceed TTL (1h)
- Break attribution: Should be intentional (content changes)

## Success Criteria

You've successfully implemented token optimization when:

✓ Second message uses cache (cache_read_tokens > 0)
✓ Cache hits last >1 hour without breaking
✓ Token costs reduced by 60-80%
✓ No unexpected cache breaks
✓ Context window warnings trigger before overflow
✓ Cache breaks are attributed to specific changes

## Common Issues and Solutions

### Issue: Cache Never Hits
**Symptoms:** cache_creation_tokens on every call, cache_read_tokens always 0

**Solutions:**
- Check CacheControl is set on system blocks
- Verify TTL and scope fields are serialized
- Look for system prompt changes between calls
- Check for tool schema instability

### Issue: Cache Breaks Every Call
**Symptoms:** Cache alternates between creation and read

**Solutions:**
- Enable cache break detection logging
- Check for dynamic content in system prompt
- Verify model string is stable
- Look for mid-session configuration changes

### Issue: High Token Costs Despite Caching
**Symptoms:** Cache works but costs still high

**Solutions:**
- Check if caching right content (AGENTS.md, not env info)
- Verify cache scope is appropriate (global vs session)
- Look for uncached tool results accumulation
- Consider enabling API-side microcompaction

### Issue: Cache Breaks After ~5 Minutes
**Symptoms:** Predictable breaks at 5-6 minute intervals

**Solutions:**
- TTL is not being set (defaulting to 5min)
- Check CacheControl.TTL = "1h" is in code
- Verify TTL field is being serialized to JSON
- Check API provider properly maps TTL to SDK

## Performance Expectations

### Typical Session (10 turns)

**Without optimization:**
```
Turn 1:  50K input + 2K output = 52K tokens
Turn 2:  50K input + 2K output = 52K tokens
Turn 3:  50K input + 2K output = 52K tokens
...
Turn 10: 50K input + 2K output = 52K tokens

Total: 520K tokens
Cost:  ~$1.75
```

**With optimization (1h TTL + global scope):**
```
Turn 1:  30K input + 20K cached + 2K output = 52K tokens
Turn 2:  7K input + 45K from cache + 2K output = 9K new tokens
Turn 3:  7K input + 45K from cache + 2K output = 9K new tokens
...
Turn 10: 7K input + 45K from cache + 2K output = 9K new tokens

Total: 124K tokens (76% reduction!)
Cost:  ~$0.35 (80% savings!)
```

### Cache Hit Rates by Turn

Turn 1: 0% (initial cache creation)
Turn 2: 85-90% (most system content cached)
Turn 3+: 90-95% (stable cache, minimal new content)

## Resources

### Internal Documentation
- Quick Start: `token-optimization-quickstart.md`
- Full Analysis: `token-optimization-analysis.md`
- Implementation: `token-optimization-implementation.md`
- Deep Dive: `claude-code-caching-deep-dive.md`

### External Resources
- [Anthropic Prompt Caching Docs](https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching)
- [Claude Code Repository](https://github.com/anthropics/claude-code)
- [Go Anthropic SDK](https://github.com/anthropics/anthropic-sdk-go)

### Claude Code Source Files
Key files to reference:
- `src/services/api/promptCacheBreakDetection.ts`
- `src/services/api/claude.ts`
- `src/utils/tokens.ts`
- `src/services/compact/apiMicrocompact.ts`

## Contributing

When adding new optimizations:

1. Document the optimization in analysis doc
2. Add implementation example to implementation doc
3. Update quick start if it's a high-impact change
4. Add metrics and monitoring
5. Test with real sessions, measure impact
6. Document gotchas and edge cases

## Version History

- **v1.0** - Initial analysis from claude-code review (April 2026)
- Comprehensive token optimization documentation
- Quick wins, full implementation guide, and deep dive

---

Questions? Start with the quick start guide and work your way through the implementation guide.

Remember: The goal is **predictable, low token costs** through smart caching, not premature optimization.
