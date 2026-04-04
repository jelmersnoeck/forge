# Token Optimization Visual Guide

ASCII diagrams showing how caching and optimizations work.

## Cache Lifecycle

```
┌──────────────────────────────────────────────────────────────────┐
│                        FIRST API CALL                             │
├──────────────────────────────────────────────────────────────────┤
│                                                                    │
│  System Prompt (50K tokens)                                       │
│  ├─ Base prompt (5K) ────────────────────> [Not cached]          │
│  ├─ Environment (1K) ────────────────────> [Not cached]          │
│  └─ AGENTS.md (20K) ─────────────────────> [CACHED] ✓            │
│      CacheControl: { type: "ephemeral", ttl: "1h" }              │
│                                                                    │
│  Tool Schemas (15K tokens) ──────────────> [CACHED] ✓            │
│                                                                    │
│  Messages (5K tokens) ───────────────────> [Not cached]          │
│                                                                    │
├──────────────────────────────────────────────────────────────────┤
│  USAGE:                                                           │
│  input_tokens: 30,000                                             │
│  cache_creation_input_tokens: 35,000 ← Creating cache            │
│  output_tokens: 2,000                                             │
│                                                                    │
│  Total billed: 65,000 tokens                                      │
│  Cost: ~$0.21                                                     │
└──────────────────────────────────────────────────────────────────┘

                              ↓ 2 minutes later

┌──────────────────────────────────────────────────────────────────┐
│                        SECOND API CALL                            │
├──────────────────────────────────────────────────────────────────┤
│                                                                    │
│  System Prompt (50K tokens)                                       │
│  ├─ Base prompt (5K) ────────────────────> [Not cached]          │
│  ├─ Environment (1K) ────────────────────> [Not cached]          │
│  └─ AGENTS.md (20K) ─────────────────────> [FROM CACHE] ⚡       │
│                                                                    │
│  Tool Schemas (15K tokens) ──────────────> [FROM CACHE] ⚡       │
│                                                                    │
│  Messages (10K tokens) ───────────────────> [Not cached]         │
│                                                                    │
├──────────────────────────────────────────────────────────────────┤
│  USAGE:                                                           │
│  input_tokens: 16,000 ← Only uncached content                    │
│  cache_read_input_tokens: 35,000 ← Reading from cache            │
│  output_tokens: 2,000                                             │
│                                                                    │
│  Total billed: 18,000 tokens (72% reduction!) 🎉                 │
│  Cost: ~$0.05 (76% savings!)                                     │
└──────────────────────────────────────────────────────────────────┘
```

## Cache Break Scenarios

### Scenario 1: Intentional Break (System Prompt Changed)

```
Call 1: System Hash: abc123 ──> cache_creation: 35K
                                
        ↓ User edits AGENTS.md
                                
Call 2: System Hash: def456 ──> cache_creation: 35K (NEW CACHE)
        [EXPECTED] System content changed
```

### Scenario 2: TTL Expiry

```
Call 1: cache_creation: 35K ──> TTL: 1h, expires at 3:47pm
                                
        ↓ Wait 65 minutes (until 3:52pm)
                                
Call 2: cache_creation: 35K ──> [EXPECTED] TTL expired
        Time since last: 65m > 60m threshold
```

### Scenario 3: Unexpected Break (BUG!)

```
Call 1: cache_creation: 35K
        System Hash: abc123
        Tool Hash: xyz789
                                
        ↓ 2 minutes, nothing changed
                                
Call 2: cache_creation: 35K ──> [UNEXPECTED!] 
        System Hash: abc123 (same)
        Tool Hash: xyz789 (same)
        Time: 2m < 5m TTL
        
        [CACHE BREAK] likely server-side (prompt unchanged, <5min gap)
```

## Token Estimation Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                   Estimating Context Size                        │
└─────────────────────────────────────────────────────────────────┘

Step 1: Find Last API Call with Usage
   messages[0..10] ──> ✗ no usage
                  ↓
   messages[11]    ──> ✓ has usage: {
                         input: 42K,
                         output: 2K,
                         cache_read: 35K
                       }
                       Total: 79K tokens

Step 2: Estimate Messages Added Since
   messages[12]: "Read file X"
      ├─ text (100 chars) ──> 100/4 = 25 tokens
      └─ tool_result (5K chars) ──> 5000/4 = 1250 tokens
   
   messages[13]: "What does it do?"
      └─ text (50 chars) ──> 50/4 = 13 tokens
   
   New messages: ~1,288 tokens

Step 3: Combine
   Base (from last call): 79,000 tokens
   + Estimated new:       +1,288 tokens
   = Current context:     ~80,288 tokens

Step 4: Check Threshold
   Current: 80,288 tokens
   Warning: 180,000 tokens
   Max:     195,000 tokens
   
   Status: ✓ OK (41% full)
```

## Cache Break Detection Flow

```
┌─────────────────────────────────────────────────────────────────┐
│              Pre-Call (recordPromptState)                        │
└─────────────────────────────────────────────────────────────────┘

1. Hash Current State
   systemHash = hash(system_blocks)      ──> "abc123"
   toolsHash = hash(tool_schemas)        ──> "xyz789"
   cacheControlHash = hash(cache_control)──> "ctl456"

2. Compare to Previous
   if (prev.systemHash != current.systemHash) {
     pendingChanges.systemPromptChanged = true
   }
   if (prev.toolsHash != current.toolsHash) {
     pendingChanges.toolSchemasChanged = true
     // Also check which specific tools changed
     changedToolSchemas = diffPerToolHashes()
   }

3. Store for Post-Call
   state.pendingChanges = pendingChanges

                           ↓ Make API Call

┌─────────────────────────────────────────────────────────────────┐
│         Post-Call (checkResponseForCacheBreak)                   │
└─────────────────────────────────────────────────────────────────┘

1. Get Cache Tokens
   prevCacheRead: 35,000 tokens
   newCacheRead:  35,000 tokens

2. Calculate Drop
   drop = 35000 - 35000 = 0 tokens
   percentDrop = 0%
   
   ✓ No cache break (drop < 5% and drop < 2000)

                           OR

1. Get Cache Tokens
   prevCacheRead: 35,000 tokens
   newCacheRead:  0 tokens

2. Calculate Drop
   drop = 35000 - 0 = 35,000 tokens
   percentDrop = 100%
   
   ✗ CACHE BREAK DETECTED!

3. Attribute Cause
   if (pendingChanges.systemPromptChanged) {
     reason = "system prompt changed"
   } else if (timeSinceLastCall > 1h) {
     reason = "1h TTL expiry"
   } else if (timeSinceLastCall > 5min) {
     reason = "5min TTL expiry"
   } else {
     reason = "likely server-side"
   }

4. Log & Alert
   [CACHE BREAK] reason
   [cache read: 35000 → 0, call #5]
```

## Global vs Session Scope

```
┌─────────────────────────────────────────────────────────────────┐
│                    GLOBAL SCOPE                                  │
└─────────────────────────────────────────────────────────────────┘

Session A                        Anthropic Server
  ├─ User: Alice                     │
  ├─ CWD: /project1                  │
  └─ System: "You are..."  ─────────>│
      CacheControl:                  │  [GLOBAL CACHE]
        scope: "global"              │  key: hash("You are...")
                                     │  shared by all sessions

Session B                            │
  ├─ User: Bob                       │
  ├─ CWD: /project2                  │
  └─ System: "You are..."  ──────────┤ (same hash!)
      CacheControl:                  │
        scope: "global"        ←──────┘ Returns cached copy!

✓ Bob's first call gets Alice's cache (if prompt identical)
✓ Saves ~35K tokens on first call
✓ ONLY use for truly universal content (base system prompt)


┌─────────────────────────────────────────────────────────────────┐
│                   SESSION SCOPE (default)                        │
└─────────────────────────────────────────────────────────────────┘

Session A                        Anthropic Server
  ├─ System: "Project X..."            │
  └─ CacheControl:           ─────────>│  [SESSION CACHE A]
      scope: omitted (default)         │  key: session_id_A + hash(...)

Session B                              │
  ├─ System: "Project X..." ──────────>│  [SESSION CACHE B]
  └─ CacheControl:                     │  key: session_id_B + hash(...)
      scope: omitted                   │  (separate cache!)

✗ Each session has own cache (no sharing)
✓ Safe for project-specific content
✓ Safe for user-specific content
```

## Cost Breakdown

```
┌─────────────────────────────────────────────────────────────────┐
│              Token Cost Comparison (10-turn session)             │
└─────────────────────────────────────────────────────────────────┘

WITHOUT CACHING (default 5min TTL, frequent breaks)
═══════════════════════════════════════════════════════════════════
Turn 1:  50K input × $3/1M       = $0.150
         2K output × $15/1M      = $0.030
                                   ──────
                                   $0.180

Turn 2:  50K input (no cache)    = $0.150
         2K output               = $0.030
                                   ──────
                                   $0.180

Turn 3-10: 8 × $0.180            = $1.440

Total: $1.80 for 10 turns

WITH CACHING (1h TTL, stable cache)
═══════════════════════════════════════════════════════════════════
Turn 1:  30K input × $3/1M       = $0.090
         35K cache × $3.75/1M    = $0.131
         2K output × $15/1M      = $0.030
                                   ──────
                                   $0.251

Turn 2:  15K input × $3/1M       = $0.045
         35K cache read × $0.3/1M= $0.011  ← 90% cheaper!
         2K output × $15/1M      = $0.030
                                   ──────
                                   $0.086

Turn 3-10: 8 × $0.086            = $0.688

Total: $1.025 for 10 turns
Savings: $0.775 (43% reduction) 💰

WITH CACHING + GLOBAL SCOPE (cache hits on first call)
═══════════════════════════════════════════════════════════════════
Turn 1:  15K input × $3/1M       = $0.045  ← No cache creation!
         35K cache read × $0.3/1M= $0.011  ← Already cached!
         2K output × $15/1M      = $0.030
                                   ──────
                                   $0.086

Turn 2-10: 9 × $0.086            = $0.774

Total: $0.86 for 10 turns
Savings: $0.94 (52% reduction) 💰💰
```

## State Latching

```
┌─────────────────────────────────────────────────────────────────┐
│                    WITHOUT State Latching                        │
└─────────────────────────────────────────────────────────────────┘

Session Start
  ├─ User: pro subscriber
  ├─ cache1hEligible: true
  └─ CacheControl: { ttl: "1h" }

     ↓ 30 minutes into session

User Hits Rate Limit
  ├─ Billing: using overage
  ├─ cache1hEligible: false ← CHANGED!
  └─ CacheControl: { ttl: omitted } ← Defaults to 5min

     ↓ Next API call

[CACHE BREAK!] TTL changed (1h → 5min)
- Previous cache invalidated
- Must recreate entire cache
- Lost 30 minutes of work
- Costs spike unexpectedly


┌─────────────────────────────────────────────────────────────────┐
│                     WITH State Latching                          │
└─────────────────────────────────────────────────────────────────┘

Session Start
  ├─ User: pro subscriber
  ├─ cache1hEligible: true ← LATCHED at init
  └─ CacheControl: { ttl: "1h" }

     ↓ 30 minutes into session

User Hits Rate Limit
  ├─ Billing: using overage
  ├─ cache1hEligible: still true ← UNCHANGED (latched!)
  └─ CacheControl: { ttl: "1h" } ← Same as before

     ↓ Next API call

✓ Cache still valid
✓ No unexpected cost spike
✓ Stable throughout session
```

## Priority Implementation Order

```
┌───────────────────────────────────────────────────────────────────┐
│                    Implementation Priority                         │
└───────────────────────────────────────────────────────────────────┘

HIGH (Do First) ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 70% of value
│
├─ 1. Add TTL to CacheControl          [5 min]   ████████████
│     Impact: 60-80% cost reduction
│
├─ 2. Enable 1h TTL on AGENTS.md       [5 min]   ████████████
│     Impact: Immediate cache benefits
│
└─ 3. Track cache tokens               [10 min]  ██████████
      Impact: Visibility into caching

MEDIUM (Do Second) ━━━━━━━━━━━━━━━━━━━━━━━━━━━ 20% of value
│
├─ 4. Cache break detection            [30 min]  ████████
│     Impact: Catch problems early
│
├─ 5. Token estimation                 [45 min]  ██████
│     Impact: Prevent context overruns
│
└─ 6. Session metrics                  [30 min]  █████
      Impact: Cost tracking

LOW (Do Later) ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 10% of value
│
├─ 7. Per-tool hash tracking           [1 hr]    ███
│     Impact: Better attribution
│
├─ 8. State latching                   [1 hr]    ███
│     Impact: Edge case handling
│
└─ 9. Context management               [2 hrs]   ██
      Impact: Future feature prep

Total time for HIGH priority: 20 minutes
Total time for MEDIUM priority: 1h 45min
Total time for LOW priority: 4+ hours

Recommendation: Start with HIGH, validate savings, then do MEDIUM.
```

## Success Validation

```
┌─────────────────────────────────────────────────────────────────┐
│                    After Implementation                          │
└─────────────────────────────────────────────────────────────────┘

Test Session:
  Turn 1 ──> [USAGE] cache_creation: 35000 ✓
             [USAGE] input: 15000, output: 2000
             
  Turn 2 ──> [USAGE] cache_read: 35000 ✓
             [USAGE] input: 7000, output: 2000
             
  Turn 3 ──> [USAGE] cache_read: 35000 ✓
             [USAGE] input: 7000, output: 2000

  (wait 30 minutes)
             
  Turn 4 ──> [USAGE] cache_read: 35000 ✓
             [NO CACHE BREAK WARNING] ✓

  (wait 65 minutes)
             
  Turn 5 ──> [USAGE] cache_creation: 35000
             [CACHE BREAK] 1h TTL expiry ✓
             (expected behavior)

Results:
✓ Cache hits on turns 2-4 (85% of tokens from cache)
✓ Cache survives 30min idle (1h TTL working)
✓ Cache expires at 65min (TTL correctly enforced)
✓ No unexpected breaks (stable system/tools)

Cost Analysis:
  Before:  $0.18 × 5 turns = $0.90
  After:   $0.25 + ($0.09 × 3) + $0.25 = $0.77
  Savings: $0.13 (14% on 5 turns)
  
  Extrapolate to 20 turns:
  Before:  $3.60
  After:   $2.05
  Savings: $1.55 (43% reduction) 🎉
```

---

These diagrams show the key concepts visually. Reference them when implementing or debugging cache behavior.
