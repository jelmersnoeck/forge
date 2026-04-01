# Token Optimization Implementation Guide

Step-by-step guide for implementing token optimizations in forge, prioritized by impact.

## Phase 1: Foundation (Essential Tracking)

### 1.1 Enhanced Token Usage Types

Add cache-aware token tracking to types:

```go
// internal/types/types.go

// Update TokenUsage to match Anthropic's response structure
type TokenUsage struct {
    InputTokens         int `json:"input_tokens"`
    OutputTokens        int `json:"output_tokens"`
    CacheCreationTokens int `json:"cache_creation_input_tokens"`
    CacheReadTokens     int `json:"cache_read_input_tokens"`
}

// New: Aggregate session usage tracking
type SessionUsageMetrics struct {
    TotalCalls          int     `json:"total_calls"`
    TotalInputTokens    int     `json:"total_input_tokens"`
    TotalOutputTokens   int     `json:"total_output_tokens"`
    TotalCacheCreated   int     `json:"total_cache_created"`
    TotalCacheRead      int     `json:"total_cache_read"`
    EstimatedCostUSD    float64 `json:"estimated_cost_usd"`
    CacheHitRate        float64 `json:"cache_hit_rate"`
}
```

### 1.2 Enhanced Cache Control

Add TTL and scope support:

```go
// internal/types/types.go

type CacheControl struct {
    Type  string `json:"type"`           // "ephemeral"
    TTL   string `json:"ttl,omitempty"`  // "1h" for extended cache
    Scope string `json:"scope,omitempty"` // "global" for cross-session caching
}
```

### 1.3 Provider Updates

Update Anthropic provider to extract full usage:

```go
// internal/runtime/provider/anthropic.go

// In the message_start handler:
case "message_start":
    usage := event.Message.Usage
    ch <- types.ChatDelta{
        Type: "usage",
        Usage: &types.TokenUsage{
            InputTokens:         int(usage.InputTokens),
            OutputTokens:        int(usage.OutputTokens),
            CacheCreationTokens: int(usage.CacheCreationInputTokens),
            CacheReadTokens:     int(usage.CacheReadInputTokens),
        },
    }

// In the message_delta handler (update output tokens):
case "message_delta":
    ch <- types.ChatDelta{
        Type: "usage_delta",
        Usage: &types.TokenUsage{
            OutputTokens: int(event.Usage.OutputTokens),
        },
    }
```

## Phase 2: Smart Caching (Big Wins)

### 2.1 1-Hour Cache TTL

Enable longer cache for better hit rates:

```go
// internal/runtime/prompt/prompt.go

func Assemble(bundle types.ContextBundle, cwd string) []types.SystemBlock {
    var blocks []types.SystemBlock
    
    // Base prompt - no cache control
    blocks = append(blocks, types.SystemBlock{
        Type: "text",
        Text: basePrompt,
    })
    
    // Environment info - no cache control (changes per session)
    blocks = append(blocks, types.SystemBlock{
        Type: "text", 
        Text: envInfo,
    })
    
    // CLAUDE.md content - CACHE THIS with 1h TTL and global scope
    if len(bundle.ClaudeMD) > 0 {
        blocks = append(blocks, types.SystemBlock{
            Type: "text",
            Text: claudeContent.String(),
            CacheControl: &types.CacheControl{
                Type:  "ephemeral",
                TTL:   "1h",      // NEW: Extended cache lifetime
                Scope: "global",  // NEW: Share across sessions
            },
        })
    }
    
    // Rules - cache without global scope (project-specific)
    if len(bundle.Rules) > 0 {
        blocks = append(blocks, types.SystemBlock{
            Type: "text",
            Text: rulesContent.String(),
            CacheControl: &types.CacheControl{
                Type: "ephemeral",
                TTL:  "1h",
            },
        })
    }
    
    // Skills and agents - no cache (can change frequently)
    // ...
    
    return blocks
}
```

### 2.2 Cache State Tracking

Track cache health per session:

```go
// internal/runtime/session/cache_tracker.go

package session

import (
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "log"
    "time"
)

type CacheTracker struct {
    systemHash       string
    toolsHash        string
    lastCacheRead    int
    lastCallTime     time.Time
    callCount        int
}

func NewCacheTracker() *CacheTracker {
    return &CacheTracker{}
}

// ComputeSystemHash hashes the system prompt for change detection
func (ct *CacheTracker) ComputeSystemHash(system []types.SystemBlock) string {
    data, _ := json.Marshal(system)
    hash := sha256.Sum256(data)
    return hex.EncodeToString(hash[:])
}

// ComputeToolsHash hashes tool schemas for change detection  
func (ct *CacheTracker) ComputeToolsHash(tools []types.ToolSchema) string {
    data, _ := json.Marshal(tools)
    hash := sha256.Sum256(data)
    return hex.EncodeToString(hash[:])
}

// CheckForBreak detects unexpected cache invalidation
func (ct *CacheTracker) CheckForBreak(
    usage types.TokenUsage,
    system []types.SystemBlock,
    tools []types.ToolSchema,
) {
    ct.callCount++
    
    // First call - just record baseline
    if ct.lastCacheRead == 0 {
        ct.lastCacheRead = usage.CacheReadTokens
        ct.systemHash = ct.ComputeSystemHash(system)
        ct.toolsHash = ct.ComputeToolsHash(tools)
        ct.lastCallTime = time.Now()
        return
    }
    
    // Check for cache break (>5% drop and >2K tokens)
    tokenDrop := ct.lastCacheRead - usage.CacheReadTokens
    if usage.CacheReadTokens < int(float64(ct.lastCacheRead)*0.95) &&
       tokenDrop > 2000 {
        
        // Determine cause
        newSystemHash := ct.ComputeSystemHash(system)
        newToolsHash := ct.ComputeToolsHash(tools)
        timeSince := time.Since(ct.lastCallTime)
        
        reasons := []string{}
        if newSystemHash != ct.systemHash {
            reasons = append(reasons, "system prompt changed")
        }
        if newToolsHash != ct.toolsHash {
            reasons = append(reasons, "tool schemas changed")
        }
        if timeSince > time.Hour {
            reasons = append(reasons, "1h TTL expired")
        } else if timeSince > 5*time.Minute {
            reasons = append(reasons, "5min TTL expired")
        }
        
        if len(reasons) == 0 {
            reasons = append(reasons, "unknown (likely server-side)")
        }
        
        log.Printf("[CACHE BREAK] Call #%d: %d → %d tokens (%s)",
            ct.callCount,
            ct.lastCacheRead,
            usage.CacheReadTokens,
            reasons)
        
        // Update hashes
        ct.systemHash = newSystemHash
        ct.toolsHash = newToolsHash
    }
    
    ct.lastCacheRead = usage.CacheReadTokens
    ct.lastCallTime = time.Now()
}
```

### 2.3 Integrate Cache Tracker

Add to conversation loop:

```go
// internal/runtime/loop/loop.go

type ConversationLoop struct {
    provider      types.LLMProvider
    session       *session.Session
    toolRegistry  *tools.Registry
    cacheTracker  *session.CacheTracker  // NEW
    // ...
}

func NewConversationLoop(...) *ConversationLoop {
    return &ConversationLoop{
        // ...
        cacheTracker: session.NewCacheTracker(),
    }
}

// In the streaming response handler:
func (l *ConversationLoop) handleResponse(deltaChan <-chan types.ChatDelta) {
    var aggregatedUsage types.TokenUsage
    
    for delta := range deltaChan {
        switch delta.Type {
        case "usage":
            // Aggregate usage as it comes in
            aggregatedUsage.InputTokens += delta.Usage.InputTokens
            aggregatedUsage.OutputTokens += delta.Usage.OutputTokens
            aggregatedUsage.CacheCreationTokens += delta.Usage.CacheCreationTokens
            aggregatedUsage.CacheReadTokens += delta.Usage.CacheReadTokens
            
            // Emit usage event
            l.emitUsageEvent(delta.Usage)
            
        // ... other cases ...
        }
    }
    
    // After response complete, check for cache breaks
    if aggregatedUsage.InputTokens > 0 {
        systemPrompt := l.buildSystemPrompt()
        toolSchemas := l.toolRegistry.GetSchemas()
        l.cacheTracker.CheckForBreak(aggregatedUsage, systemPrompt, toolSchemas)
    }
}
```

## Phase 3: Token Estimation (Context Management)

### 3.1 Token Estimation Utilities

```go
// internal/runtime/loop/token_estimation.go

package loop

import (
    "encoding/json"
    "github.com/jelmersnoeck/forge/internal/types"
)

const (
    CharsPerToken = 4     // Rough estimate: 4 chars ≈ 1 token
    ImageTokens   = 2000  // Images cost ~2000 tokens regardless of size
)

// RoughEstimate converts characters to approximate token count
func RoughEstimate(text string) int {
    return len(text) / CharsPerToken
}

// EstimateMessageTokens estimates tokens for a set of messages
func EstimateMessageTokens(messages []types.ChatMessage) int {
    total := 0
    
    for _, msg := range messages {
        for _, block := range msg.Content {
            total += EstimateBlockTokens(block)
        }
    }
    
    return total
}

// EstimateBlockTokens estimates tokens for a single content block
func EstimateBlockTokens(block types.ChatContentBlock) int {
    switch block.Type {
    case "text":
        return RoughEstimate(block.Text)
        
    case "tool_use":
        inputJSON, _ := json.Marshal(block.Input)
        return RoughEstimate(block.Name + string(inputJSON))
        
    case "tool_result":
        total := 0
        for _, result := range block.Content {
            if result.Type == "text" {
                total += RoughEstimate(result.Text)
            } else if result.Type == "image" {
                total += ImageTokens
            }
        }
        return total
        
    default:
        return 0
    }
}

// EstimateSystemTokens estimates tokens for system prompt
func EstimateSystemTokens(system []types.SystemBlock) int {
    total := 0
    for _, block := range system {
        total += RoughEstimate(block.Text)
    }
    return total
}

// EstimateToolSchemaTokens estimates tokens for tool definitions
func EstimateToolSchemaTokens(tools []types.ToolSchema) int {
    toolsJSON, _ := json.Marshal(tools)
    return RoughEstimate(string(toolsJSON))
}
```

### 3.2 Context Window Tracking

```go
// internal/runtime/session/session.go

// Add to Session struct
type Session struct {
    // ... existing fields ...
    
    usageMetrics SessionUsageMetrics
    lastUsage    *types.TokenUsage
    lastCallIdx  int  // Index of last message with usage
}

// EstimateCurrentTokens returns estimated total context size
func (s *Session) EstimateCurrentTokens(systemBlocks []types.SystemBlock, tools []types.ToolSchema) int {
    // If we have usage from last API call, use it as baseline
    if s.lastUsage != nil {
        baseTokens := s.lastUsage.InputTokens +
            s.lastUsage.OutputTokens +
            s.lastUsage.CacheCreationTokens +
            s.lastUsage.CacheReadTokens
        
        // Add estimate for messages added since last call
        newMessages := s.messages[s.lastCallIdx+1:]
        estimatedNew := loop.EstimateMessageTokens(newMessages)
        
        return baseTokens + estimatedNew
    }
    
    // No usage yet - estimate everything
    total := 0
    total += loop.EstimateSystemTokens(systemBlocks)
    total += loop.EstimateToolSchemaTokens(tools)
    total += loop.EstimateMessageTokens(s.messages)
    
    // Pad by 33% to be conservative
    return total * 4 / 3
}

// UpdateUsage records token usage and updates metrics
func (s *Session) UpdateUsage(usage types.TokenUsage) {
    s.lastUsage = &usage
    s.lastCallIdx = len(s.messages) - 1
    
    s.usageMetrics.TotalCalls++
    s.usageMetrics.TotalInputTokens += usage.InputTokens
    s.usageMetrics.TotalOutputTokens += usage.OutputTokens
    s.usageMetrics.TotalCacheCreated += usage.CacheCreationTokens
    s.usageMetrics.TotalCacheRead += usage.CacheReadTokens
    
    // Calculate cache hit rate
    totalCacheOpportunity := s.usageMetrics.TotalCacheCreated + s.usageMetrics.TotalCacheRead
    if totalCacheOpportunity > 0 {
        s.usageMetrics.CacheHitRate = float64(s.usageMetrics.TotalCacheRead) / float64(totalCacheOpportunity)
    }
    
    // Estimate cost (example rates, update to actual)
    s.usageMetrics.EstimatedCostUSD += calculateCost(usage)
}

func calculateCost(usage types.TokenUsage) float64 {
    // Example pricing (update to actual rates for your model)
    const (
        inputCostPer1M        = 3.0  // $3 per 1M input tokens
        outputCostPer1M       = 15.0 // $15 per 1M output tokens  
        cacheCreateCostPer1M  = 3.75 // $3.75 per 1M cache write tokens
        cacheReadCostPer1M    = 0.30 // $0.30 per 1M cache read tokens
    )
    
    cost := 0.0
    cost += float64(usage.InputTokens) / 1_000_000 * inputCostPer1M
    cost += float64(usage.OutputTokens) / 1_000_000 * outputCostPer1M
    cost += float64(usage.CacheCreationTokens) / 1_000_000 * cacheCreateCostPer1M
    cost += float64(usage.CacheReadTokens) / 1_000_000 * cacheReadCostPer1M
    
    return cost
}
```

### 3.3 Context Window Warnings

```go
// internal/runtime/loop/loop.go

const (
    ContextWarningThreshold = 180_000  // Warn at 180K tokens
    ContextMaxThreshold     = 195_000  // Hard stop at 195K tokens
)

func (l *ConversationLoop) checkContextWindow() error {
    currentTokens := l.session.EstimateCurrentTokens(
        l.buildSystemPrompt(),
        l.toolRegistry.GetSchemas(),
    )
    
    if currentTokens > ContextMaxThreshold {
        return fmt.Errorf("context window full (%d tokens), compaction required", currentTokens)
    }
    
    if currentTokens > ContextWarningThreshold {
        l.emit(types.OutboundEvent{
            Type:    "warning",
            Content: fmt.Sprintf("Context window at %d tokens (%.0f%% full)",
                currentTokens,
                float64(currentTokens)/float64(ContextMaxThreshold)*100),
        })
    }
    
    return nil
}
```

## Phase 4: Advanced Optimizations

### 4.1 API Context Management (Future)

Prepare structure for server-side compaction:

```go
// internal/types/types.go

// ContextManagement enables API-side context cleanup (beta feature)
type ContextManagement struct {
    Edits []ContextEditStrategy `json:"edits"`
}

type ContextEditStrategy struct {
    Type            string        `json:"type"` // "clear_tool_uses_20250919"
    Trigger         *TokenTrigger `json:"trigger,omitempty"`
    ClearAtLeast    *TokenTrigger `json:"clear_at_least,omitempty"`
    ClearToolInputs []string      `json:"clear_tool_inputs,omitempty"`
}

type TokenTrigger struct {
    Type  string `json:"type"`  // "input_tokens"
    Value int    `json:"value"`
}

// Add to ChatRequest when ready to use beta
type ChatRequest struct {
    Model              string             `json:"model"`
    System             []SystemBlock      `json:"system"`
    Messages           []ChatMessage      `json:"messages"`
    Tools              []ToolSchema       `json:"tools"`
    MaxTokens          int                `json:"max_tokens"`
    ContextManagement  *ContextManagement `json:"context_management,omitempty"` // NEW
}
```

### 4.2 Environment-Based Configuration

```go
// internal/runtime/config/cache_config.go

package config

import (
    "os"
    "strconv"
)

type CacheConfig struct {
    EnableCache       bool
    Use1HourTTL       bool
    UseGlobalScope    bool
    TrackCacheBreaks  bool
    WarningThreshold  int
}

func LoadCacheConfig() CacheConfig {
    return CacheConfig{
        EnableCache:      !envTruthy("FORGE_DISABLE_CACHE"),
        Use1HourTTL:      envTruthy("FORGE_CACHE_1H_TTL") || envTruthy("ANTHROPIC_API_KEY"), // Default on for direct users
        UseGlobalScope:   envTruthy("FORGE_CACHE_GLOBAL_SCOPE"),
        TrackCacheBreaks: envTruthy("FORGE_TRACK_CACHE_BREAKS") || envTruthy("FORGE_DEBUG"),
        WarningThreshold: envInt("FORGE_CONTEXT_WARNING", 180_000),
    }
}

func envTruthy(key string) bool {
    val := os.Getenv(key)
    return val == "1" || val == "true" || val == "True" || val == "TRUE"
}

func envInt(key string, defaultVal int) int {
    val := os.Getenv(key)
    if val == "" {
        return defaultVal
    }
    i, err := strconv.Atoi(val)
    if err != nil {
        return defaultVal
    }
    return i
}
```

## Testing the Implementation

### Test 1: Cache Hit Verification

```bash
# Start agent
./forge-agent --port 8080

# Send first message (should see cache_creation_tokens)
curl -X POST http://localhost:8080/messages \
  -d '{"sessionId":"test1","text":"Hello"}'

# Send second message (should see cache_read_tokens)  
curl -X POST http://localhost:8080/messages \
  -d '{"sessionId":"test1","text":"What files are in the current directory?"}'

# Check logs for:
# [USAGE] input_tokens: X, cache_creation_tokens: Y (first call)
# [USAGE] input_tokens: X, cache_read_tokens: Y (second call)
```

### Test 2: Cache Break Detection

```bash
# Modify CLAUDE.md between calls
echo "# New rule" >> CLAUDE.md

# Next message should trigger cache break warning:
# [CACHE BREAK] Call #3: 5000 → 0 tokens (system prompt changed)
```

### Test 3: Token Estimation

```bash
# Enable debug logging
export FORGE_DEBUG=1

# Should see estimated vs actual token counts:
# [DEBUG] Estimated context: 45000 tokens
# [USAGE] Actual context: 42891 tokens (95% accurate)
```

---

## Summary Checklist

- [ ] Enhanced TokenUsage type with cache fields
- [ ] CacheControl with TTL and Scope support
- [ ] Provider extracts all usage fields
- [ ] 1h TTL enabled on CLAUDE.md blocks
- [ ] Global scope on appropriate blocks
- [ ] CacheTracker implementation
- [ ] Cache break detection integrated
- [ ] Token estimation utilities
- [ ] Context window tracking
- [ ] Usage metrics per session
- [ ] Cost calculation
- [ ] Warning thresholds
- [ ] Environment configuration
- [ ] Debug logging for cache events

## Performance Impact

Expected improvements after full implementation:

- **Cache hit rate:** 70-90% after first turn
- **Token cost reduction:** 60-80% for cached content
- **Session cost savings:** 50-70% for typical sessions
- **Context awareness:** Real-time tracking prevents overruns

Example session (with 1h TTL + global scope):
```
Call 1: 50K input (20K cached) + 2K output = 52K tokens
Call 2: 50K input (45K from cache!) + 2K output = ~7K new tokens  
Call 3: 50K input (45K from cache!) + 2K output = ~7K new tokens
...

Total for 10 calls WITHOUT cache: 520K tokens
Total for 10 calls WITH cache: 115K tokens (78% savings!)
```
