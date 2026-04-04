# Token Optimization Analysis: claude-code → forge

Based on reviewing claude-code's implementation, here are the key token optimizations we should adopt in forge.

## 1. Prompt Cache Break Detection & Monitoring

**What claude-code does:**
- Tracks system prompt hash, tool schema hash, and cache_control hash across API calls
- Detects when cache breaks occur (cache_read_tokens drops >5% and >2K tokens)
- Logs detailed analytics about WHY cache broke (system changed, tools changed, model changed, etc.)
- Tracks TTL-based cache expiration (5min vs 1h)
- Per-tool hash tracking to identify which tool schema changed
- Handles special cases (microcompact deletions, compaction)

**Implementation:** `src/services/api/promptCacheBreakDetection.ts`

**Why it matters:** Helps identify unintentional cache breaks that waste tokens. Even small prompt changes can invalidate the entire cache prefix.

**For forge:**
```go
// Add to internal/runtime/loop/cache_tracking.go
type CacheState struct {
    SystemHash         string
    ToolsHash          string
    CacheControlHash   string
    PrevCacheReadTokens int
    CallCount          int
    LastCallTime       time.Time
}

// Track before each API call, check after response
func (l *ConversationLoop) trackCacheBreak(usage TokenUsage) {
    // Detect unexpected cache misses
    // Log analytics about what changed
}
```

## 2. Tool-Based vs System-Prompt Caching Strategy

**What claude-code does:**
- Two caching strategies: `tool_based` and `system_prompt`
- Uses `global` cache scope when no MCP tools present
- Switches dynamically based on tool discovery
- Beta header: `prompt-caching-scopes-2024-08-01`

**Implementation:** `src/utils/betas.ts`, `src/services/api/claude.ts`

**Why it matters:** Global scope allows cache sharing across sessions for identical system prompts. Tool-based caching is more stable when tool schemas change frequently.

**For forge:**
```go
// Add to types/types.go
type CacheControl struct {
    Type  string `json:"type"`  // "ephemeral"
    TTL   string `json:"ttl,omitempty"`  // "1h" for eligible users
    Scope string `json:"scope,omitempty"` // "global" or omit
}

// In prompt assembly
func determineCacheStrategy(hasToolSchemaChanges bool) string {
    if hasToolSchemaChanges {
        return "tool_based" // cache after tools
    }
    return "system_prompt" // cache system, use global scope
}
```

## 3. 1-Hour Cache TTL for Eligible Users

**What claude-code does:**
- Default 5min TTL → 1h TTL for ants and subscribers
- Latches eligibility at session start (prevents mid-session cache breaks)
- GrowthBook allowlist for which query sources get 1h TTL
- Bedrock users opt-in via `ENABLE_PROMPT_CACHING_1H_BEDROCK`

**Implementation:** `src/services/api/claude.ts:393-434`

**Why it matters:** 1h TTL dramatically reduces token costs for long-running sessions. Without latching, overage state changes break the cache.

**For forge:**
```go
// Add session-stable TTL decision
type SessionState struct {
    Cache1hEligible bool
    // Set once at session start, never changed
}

func getCacheControl() CacheControl {
    ttl := "" // default 5min
    if sessionState.Cache1hEligible {
        ttl = "1h"
    }
    return CacheControl{Type: "ephemeral", TTL: ttl}
}
```

## 4. API-Side Microcompaction (Context Management)

**What claude-code does:**
- Sends `context_management` config to API for server-side cleanup
- `clear_tool_uses_20250919`: Clears old tool results when context hits threshold
- `clear_thinking_20251015`: Preserves thinking blocks in previous turns
- Ant-only feature with configurable thresholds
- Beta header: `context-management-2024-12-04`

**Implementation:** `src/services/compact/apiMicrocompact.ts`

**Why it matters:** Offloads compaction logic to API, more efficient than client-side message manipulation. Reduces prompt size without breaking cache.

**For forge:**
```go
// Add to ChatRequest
type ContextManagement struct {
    Edits []ContextEditStrategy `json:"edits,omitempty"`
}

type ContextEditStrategy struct {
    Type       string              `json:"type"` // "clear_tool_uses_20250919"
    Trigger    *TokenTrigger       `json:"trigger,omitempty"`
    ClearAtLeast *TokenTrigger     `json:"clear_at_least,omitempty"`
    ClearToolInputs []string        `json:"clear_tool_inputs,omitempty"`
}

// Example usage
contextMgmt := &ContextManagement{
    Edits: []ContextEditStrategy{{
        Type: "clear_tool_uses_20250919",
        Trigger: &TokenTrigger{Type: "input_tokens", Value: 180000},
        ClearAtLeast: &TokenTrigger{Type: "input_tokens", Value: 140000},
        ClearToolInputs: []string{"Bash", "FileRead", "Grep", "Glob"},
    }},
}
```

## 5. Token Estimation for Context Window Management

**What claude-code does:**
- Uses last API response usage as baseline
- Rough estimation for new messages added since last call (chars / 4)
- Handles parallel tool calls correctly (walks back to first sibling with same message.id)
- Separate functions for different purposes:
  - `tokenCountWithEstimation()` - canonical for thresholds
  - `tokenCountFromLastAPIResponse()` - exact from last response
  - `messageTokenCountFromLastAPIResponse()` - output only

**Implementation:** `src/utils/tokens.ts`

**Why it matters:** Accurate context tracking prevents overruns and enables smart compaction triggers.

**For forge:**
```go
// Add to session/session.go
func (s *Session) EstimateTokenCount() int {
    // Find last usage
    lastUsage := s.findLastUsage()
    if lastUsage == nil {
        return s.roughEstimate()
    }
    
    // Total from last response + estimate new messages
    baseTokens := lastUsage.InputTokens + lastUsage.OutputTokens +
        lastUsage.CacheCreationTokens + lastUsage.CacheReadTokens
    
    newTokens := s.estimateMessagesSince(lastUsage.MessageIndex)
    return baseTokens + newTokens
}

func roughTokenEstimate(text string) int {
    return len(text) / 4  // Simple char-to-token ratio
}
```

## 6. Extra Body Parameters for Advanced Features

**What claude-code does:**
- `CLAUDE_CODE_EXTRA_BODY` env var for custom API params
- Used for anti-distillation, internal experiments, etc.
- Merged with beta headers for Bedrock
- Hash tracked for cache break detection

**Implementation:** `src/services/api/claude.ts:272-331`

**For forge:**
```go
// Add optional ExtraParams field
type ChatRequest struct {
    Model       string                 `json:"model"`
    System      []SystemBlock          `json:"system"`
    Messages    []ChatMessage          `json:"messages"`
    Tools       []ToolSchema           `json:"tools"`
    MaxTokens   int                    `json:"max_tokens"`
    ExtraParams map[string]interface{} `json:"-"` // Merged at send time
}

// Parse from env
func parseExtraBodyParams() map[string]interface{} {
    if body := os.Getenv("FORGE_EXTRA_BODY"); body != "" {
        var params map[string]interface{}
        json.Unmarshal([]byte(body), &params)
        return params
    }
    return nil
}
```

## 7. Per-Session Cache State Latching

**What claude-code does:**
- Latches key cache-affecting states at session start:
  - Fast mode header
  - AFK mode header  
  - Cache editing header
  - Thinking clear mode
  - 1h TTL eligibility
- Prevents mid-session changes from breaking cache

**Implementation:** `src/bootstrap/state.ts`, `src/services/api/claude.ts`

**Why it matters:** State flips (e.g., user goes into overage mid-session) would invalidate cache if not latched.

**For forge:**
```go
// Session-stable state in internal/agent/hub.go
type SessionCache struct {
    FastModeLatched       bool
    Cache1hLatched        bool
    ModelLatched          string
    // Set once, never changed during session
}

func (h *Hub) initializeCacheState() {
    h.cacheState = SessionCache{
        Cache1hLatched: isCache1hEligible(),
        ModelLatched: getDefaultModel(),
    }
}
```

## 8. Cost Tracking & Token Usage Analytics

**What claude-code does:**
- Tracks cache creation, cache read, input, and output tokens separately
- Calculates USD cost per request
- Logs detailed analytics about cache efficiency
- Separate tracking for different query sources (main thread, subagents, etc.)

**Implementation:** `src/services/api/logging.ts`, `src/cost-tracker.ts`

**For forge:**
```go
// Add to types/types.go
type TokenUsageDetail struct {
    InputTokens         int     `json:"input_tokens"`
    OutputTokens        int     `json:"output_tokens"`
    CacheCreationTokens int     `json:"cache_creation_tokens"`
    CacheReadTokens     int     `json:"cache_read_tokens"`
    EstimatedCostUSD    float64 `json:"estimated_cost_usd"`
}

// Track per session
type SessionMetrics struct {
    TotalCalls          int
    TotalInputTokens    int
    TotalCacheReadTokens int
    TotalCost           float64
    CacheHitRate        float64
}
```

## 9. Tool Schema Caching Optimization

**What claude-code does:**
- Computes per-tool hash to identify which tool changed
- Only recomputes hashes when aggregate tools hash changes
- MCP tool names sanitized to 'mcp' to avoid PII in analytics
- Tracks added/removed/changed tools separately

**Implementation:** `promptCacheBreakDetection.ts:187-196`

**For forge:**
```go
// Cache tool schemas more intelligently
type ToolCacheTracker struct {
    toolHashes map[string]string  // tool name -> hash
    aggregate  string             // hash of all tools
}

func (t *ToolCacheTracker) HasChanged(tools []ToolSchema) (bool, []string) {
    newAggregate := hashTools(tools)
    if newAggregate == t.aggregate {
        return false, nil
    }
    
    // Identify which specific tools changed
    changed := []string{}
    for _, tool := range tools {
        h := hashTool(tool)
        if t.toolHashes[tool.Name] != h {
            changed = append(changed, tool.Name)
        }
    }
    return true, changed
}
```

## 10. Structured Token Budget (Extended Thinking)

**What claude-code does:**
- API `task_budget` parameter for token-aware models
- Tracks `remaining` budget across compaction boundaries
- Uses final context window size (not billing tokens) for budget countdown
- Beta header: `task-budgets-2026-03-13`

**Implementation:** `claude.ts:479-501`, `tokens.ts:79-112`

**For forge:** 
This is cutting-edge (EAP only), but the structure is:
```go
type TaskBudget struct {
    Type      string `json:"type"` // "tokens"
    Total     int    `json:"total"`
    Remaining int    `json:"remaining,omitempty"`
}

// Add to ChatRequest
TaskBudget *TaskBudget `json:"task_budget,omitempty"`
```

---

## Priority Recommendations for Forge

### High Priority (Immediate Impact)
1. **Cache Break Detection** - Essential for debugging token costs
2. **1h TTL Support** - 12x longer cache = huge savings for long sessions
3. **Token Estimation** - Need accurate context window tracking
4. **Global Cache Scope** - Share cache across sessions

### Medium Priority (Optimization)
5. **API Microcompaction** - Let server handle cleanup efficiently
6. **Session Cache Latching** - Prevent accidental cache breaks
7. **Cost Tracking** - Visibility into token spend

### Low Priority (Advanced)
8. **Extra Body Params** - Flexibility for experiments
9. **Per-Tool Hash Tracking** - Detailed cache break attribution
10. **Task Budget** - Future-proofing for extended thinking

---

## Quick Wins for Forge

### 1. Add TTL support to CacheControl (15 min)
```go
// types/types.go
type CacheControl struct {
    Type  string `json:"type"`
    TTL   string `json:"ttl,omitempty"` // "1h"
    Scope string `json:"scope,omitempty"` // "global"
}

// prompt/prompt.go - update AGENTS.md block
CacheControl: &types.CacheControl{
    Type: "ephemeral",
    TTL:  "1h",  // Hard-code for now, make configurable later
    Scope: "global",
}
```

### 2. Track cache tokens in session (30 min)
```go
// session/session.go - extend TokenUsage tracking
type SessionUsage struct {
    Calls               int
    TotalInputTokens    int
    TotalCacheRead      int
    LastCacheRead       int
}

func (s *Session) TrackUsage(usage types.TokenUsage) {
    s.usage.Calls++
    s.usage.TotalInputTokens += usage.InputTokens
    s.usage.TotalCacheRead += usage.CacheReadTokens
    
    // Detect cache break
    if s.usage.LastCacheRead > 0 && 
       usage.CacheReadTokens < s.usage.LastCacheRead*0.95 {
        log.Printf("Cache break detected: %d → %d tokens",
            s.usage.LastCacheRead, usage.CacheReadTokens)
    }
    s.usage.LastCacheRead = usage.CacheReadTokens
}
```

### 3. Add rough token estimation (20 min)
```go
// runtime/loop/estimation.go
func EstimateTokens(messages []types.ChatMessage) int {
    total := 0
    for _, msg := range messages {
        for _, block := range msg.Content {
            switch block.Type {
            case "text":
                total += len(block.Text) / 4
            case "tool_use":
                input, _ := json.Marshal(block.Input)
                total += (len(block.Name) + len(input)) / 4
            case "tool_result":
                // Roughly 2000 tokens per image, text by length
                for _, r := range block.Content {
                    if r.Type == "text" {
                        total += len(r.Text) / 4
                    } else {
                        total += 2000
                    }
                }
            }
        }
    }
    return total
}
```

---

## Testing Cache Optimizations

Once implemented, test with:
```bash
# Enable debug logging
export FORGE_DEBUG=true

# Watch for cache metrics in agent logs
# Should see:
# - cache_creation_tokens on first call
# - cache_read_tokens on subsequent calls
# - Cache break warnings if prompt changes

# Long session test (verify 1h TTL)
# Start session, wait 10 minutes, send another message
# Should still see cache_read_tokens (not cache_creation)
```

---

## References

Key claude-code files:
- `src/services/api/promptCacheBreakDetection.ts` - Cache tracking
- `src/services/api/claude.ts` - API request assembly  
- `src/utils/tokens.ts` - Token estimation utilities
- `src/services/compact/apiMicrocompact.ts` - Context management
- `src/utils/betas.ts` - Feature flags and beta headers

Anthropic API docs:
- Prompt Caching: https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching
- Context Management: (Internal beta, not public yet)
