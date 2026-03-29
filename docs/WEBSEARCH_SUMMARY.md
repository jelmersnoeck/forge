# WebSearch Tool - Summary

## What Was Added

A new `WebSearch` tool that allows the agent to search the web for current information, documentation, and anything not in its training data.

## Key Features

### 1. Two Search Providers

**DuckDuckGo (Default)**
- ✅ No API key required
- ✅ Free, unlimited
- ✅ Works out of the box
- ❌ Limited results (instant answers only)

**Brave Search API (Recommended)**
- ✅ High-quality search results
- ✅ More comprehensive
- ✅ Free tier: 2,000 queries/month
- ❌ Requires API key

### 2. Automatic Usage

Claude will use WebSearch when it needs to:
- Look up current information
- Find documentation
- Research error messages
- Get latest versions
- Verify information

### 3. Structured Output

Results formatted as:
```
Search results for: <query>

1. <Title>
   <URL>
   <Description>

2. <Title>
   <URL>
   <Description>

...
```

## Setup

### Option 1: DuckDuckGo (Default)
```bash
# No setup needed! Works immediately
```

### Option 2: Brave Search
```bash
# 1. Get API key from https://brave.com/search/api/
# 2. Add to .env
echo "SEARCH_PROVIDER=brave" >> .env
echo "BRAVE_API_KEY=your_key_here" >> .env

# 3. Restart server
just dev-server
```

## Usage Examples

### Example 1: Documentation Lookup
```
User: "How do I use context in Bubble Tea?"

Agent:
  [WebSearch] query="Bubble Tea context usage Go"
  
  Search results for: Bubble Tea context usage Go
  1. Bubble Tea Documentation
     https://github.com/charmbracelet/bubbletea#context
     ...
  
  Based on the docs, here's how...
```

### Example 2: Current Information
```
User: "What's the latest stable Go version?"

Agent:
  [WebSearch] query="Go programming language latest stable version"
  
  The latest stable version is Go 1.22.
```

### Example 3: Error Research
```
User: "I'm getting 'context deadline exceeded'"

Agent:
  [WebSearch] query="Go context deadline exceeded causes"
  
  This error occurs when... Here are solutions:
  1. Increase timeout
  2. Check network latency
  ...
```

## Implementation

### Files Created
- `internal/tools/websearch.go` - Implementation
- `internal/tools/websearch_test.go` - Tests
- `docs/WEBSEARCH_TOOL.md` - Documentation

### Files Modified
- `internal/tools/registry.go` - Registered tool
- `.env.example` - Added search config
- `README.md` - Added WebSearch to tools list
- `QUICKREF.md` - Added WebSearch reference

### Tool Schema
```go
{
    Name: "WebSearch",
    Description: "Search the web for information...",
    InputSchema: {
        "query": string (required),
        "num_results": number (optional, default 5, max 10)
    },
    ReadOnly: true,
    Destructive: false
}
```

## How It Works

### DuckDuckGo Flow
```
1. User asks question
2. Claude decides to search
3. Tool calls DuckDuckGo Instant Answer API
4. API returns abstract + related topics
5. Results formatted and returned
6. Claude uses info to answer user
```

### Brave Flow
```
1. User asks question
2. Claude decides to search
3. Tool calls Brave Search API with API key
4. API returns web search results
5. Results formatted and returned
6. Claude uses info to answer user
```

### Error Handling
- Missing API key → Helpful error with setup instructions
- Network failure → Timeout after 30s with error message
- No results → Friendly message suggesting query refinement
- Rate limit → API error message passed through

## Configuration

### Environment Variables
```bash
# Search provider (optional, default: duckduckgo)
SEARCH_PROVIDER=duckduckgo  # or "brave"

# Brave API key (required if using Brave)
BRAVE_API_KEY=your_api_key_here
```

### Provider Selection Logic
```go
provider := os.Getenv("SEARCH_PROVIDER")
if provider == "" {
    provider = "duckduckgo" // default
}

switch provider {
case "brave":
    results, err = searchBrave(query, numResults)
case "duckduckgo":
    results, err = searchDuckDuckGo(query, numResults)
}
```

## Testing

### Unit Tests
```bash
# Test tool definition
go test ./internal/tools/ -run TestWebSearchTool

# Test truncation
go test ./internal/tools/ -run TestSearchResultTruncation

# Test DuckDuckGo (requires network)
go test ./internal/tools/ -run TestWebSearchDuckDuckGo

# Test Brave (requires BRAVE_API_KEY)
BRAVE_API_KEY=your_key go test ./internal/tools/ -run TestWebSearchBrave
```

### Manual Test
```bash
# Start server
just dev-server

# In CLI
> What's the latest version of Go?

# Expected:
[WebSearch] query="Go latest version"
<results>
```

## Benefits

### For Users
- ✅ Agent can look up current info
- ✅ No need to paste documentation
- ✅ Agent stays up-to-date
- ✅ Better error research
- ✅ More accurate answers

### For Development
- ✅ Agent can research APIs
- ✅ Find best practices
- ✅ Discover solutions
- ✅ Verify information
- ✅ Stay current with tech

## Use Cases

### Documentation Research
```
"How do I use the Anthropic streaming API?"
"What are the Bubble Tea key binding options?"
"Show me examples of Go generics"
```

### Version Checking
```
"What's the latest Node.js LTS version?"
"When was Python 3.12 released?"
"Is Go 1.22 stable yet?"
```

### Error Debugging
```
"What causes EADDRINUSE error?"
"How to fix 'module not found' in Go?"
"Why am I getting 403 Forbidden?"
```

### Best Practices
```
"What are Go error handling best practices?"
"How should I structure a REST API?"
"What's the recommended way to handle auth?"
```

### Current Events
```
"What are the latest Anthropic API features?"
"Has the Go team announced Go 2?"
"What's new in the latest Docker release?"
```

## Comparison with Alternatives

### WebSearch vs Bash + curl
**WebSearch:**
- ✅ Structured output
- ✅ Multiple results
- ✅ Built-in error handling
- ✅ Provider abstraction

**Bash + curl:**
- ✅ More flexible
- ✅ Custom API endpoints
- ❌ Manual parsing
- ❌ More error-prone

### When to Use Each

**Use WebSearch for:**
- General information lookup
- Documentation search
- Error message research
- Version checking
- Best practices

**Use Bash + curl for:**
- Specific API endpoints
- Custom data formats
- Complex parsing needs
- Non-search HTTP requests

## Privacy & Security

### DuckDuckGo
- Privacy-focused search engine
- No user tracking
- No personal data collection
- HTTPS encrypted

### Brave
- Privacy-first search
- No personal data logging
- Anonymous API requests
- HTTPS encrypted

### Forge
- Queries not logged by Forge
- No data persistence
- API keys stored in environment only
- No telemetry

## Cost Analysis

### DuckDuckGo
- **Cost:** $0
- **Limit:** Unlimited (be respectful)
- **Quality:** Basic (instant answers)

### Brave Free Tier
- **Cost:** $0
- **Limit:** 2,000 queries/month
- **Quality:** High (full web search)

### Brave Paid Tier
- **Cost:** $500/month
- **Limit:** 2M queries/month
- **Quality:** High (full web search)

### Typical Usage
For normal agent usage, free tier is more than sufficient:
- Average: 10-50 searches per day
- Free tier: ~65 searches per day
- Plenty of headroom

## Limitations

### Current Limitations
1. **No page content fetching** - Returns URLs only, not full page text
2. **No image search** - Text results only
3. **No date filtering** - Can't restrict by date
4. **No site-specific** - Can't limit to specific domains
5. **No caching** - Every search hits API

### Workarounds
```bash
# Get page content after search
[WebSearch] query="Go best practices"
[Bash] curl https://url-from-results

# Site-specific search
[WebSearch] query="site:golang.org error handling"

# Date-specific info
[WebSearch] query="Go 1.22 release date 2024"
```

## Future Enhancements

Possible improvements:
1. **Page content fetching** - Fetch full page after search
2. **Result caching** - Cache results to avoid duplicate searches
3. **More providers** - Google, Bing, etc.
4. **Image search** - Return image URLs
5. **Advanced filters** - Date, site, type, etc.
6. **Search history** - Track what agent has searched
7. **Smart query refinement** - Suggest better queries
8. **Related searches** - Show related queries

## Troubleshooting

### "BRAVE_API_KEY not set"
**Solution:** Get API key from https://brave.com/search/api/
```bash
export BRAVE_API_KEY=your_key
```

### "No results from DuckDuckGo"
**Solution:** Use Brave for better results, or refine query
```bash
export SEARCH_PROVIDER=brave
export BRAVE_API_KEY=your_key
```

### "Request timeout"
**Solution:** Check internet connection, try simpler query

### "Invalid API key"
**Solution:** Verify key has no spaces/quotes, regenerate if needed

### "Rate limit exceeded"
**Solution:** Using too many searches, upgrade to paid tier or use DuckDuckGo

## Documentation

- **Full guide:** `docs/WEBSEARCH_TOOL.md`
- **Implementation:** `internal/tools/websearch.go`
- **Tests:** `internal/tools/websearch_test.go`
- **Quick ref:** `QUICKREF.md`

## Summary

The WebSearch tool gives the agent the ability to look up current information, making it significantly more useful for:
- Research tasks
- Documentation lookup
- Error debugging
- Version checking
- Staying current

With two providers (DuckDuckGo free, Brave for quality), it's flexible and easy to set up. 🚀
