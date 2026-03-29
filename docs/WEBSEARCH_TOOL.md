# WebSearch Tool

## Overview

The WebSearch tool allows the agent to search the web for current information, documentation, API references, error messages, and anything not in its training data.

## When Claude Uses It

Claude will automatically use WebSearch when it needs to:
- Look up current/recent information
- Find documentation for libraries or APIs
- Research error messages
- Get the latest version numbers
- Find best practices for specific technologies
- Verify information that may have changed

## Configuration

### Option 1: DuckDuckGo (Default - No API Key)

No configuration needed! Just works out of the box.

```bash
# DuckDuckGo is the default, no setup required
```

**Pros:**
- ✅ No API key needed
- ✅ Free
- ✅ No rate limits

**Cons:**
- ❌ Limited to instant answers and related topics
- ❌ May return fewer results
- ❌ Not as comprehensive as full web search

### Option 2: Brave Search API (Recommended)

Better search quality with more results.

```bash
# 1. Get a free API key from https://brave.com/search/api/
# Free tier: 2,000 queries/month

# 2. Set environment variables
export SEARCH_PROVIDER=brave
export BRAVE_API_KEY=your_api_key_here
```

**Pros:**
- ✅ High-quality search results
- ✅ More comprehensive
- ✅ Better relevance
- ✅ Free tier available (2,000/month)

**Cons:**
- ❌ Requires API key
- ❌ Has rate limits

## Usage Examples

### Example 1: Look Up Documentation

```
User: "How do I use context in Bubble Tea?"

Agent:
  [WebSearch] query="Bubble Tea context usage Go"
  
  Search results for: Bubble Tea context usage Go
  
  1. Bubble Tea Documentation - Context
     https://github.com/charmbracelet/bubbletea#context
     Pass context through your Bubble Tea application...
  
  2. Context in Bubble Tea Tutorial
     https://charm.sh/blog/context-in-bubbletea
     Learn how to properly handle context cancellation...
  
  Based on the documentation, here's how to use context...
```

### Example 2: Check Latest Version

```
User: "What's the latest stable version of Go?"

Agent:
  [WebSearch] query="Go programming language latest stable version"
  
  Search results for: Go programming language latest stable version
  
  1. Go Releases - The Go Programming Language
     https://go.dev/doc/devel/release
     Go 1.22 is the latest stable version...
  
  The latest stable version is Go 1.22.
```

### Example 3: Research Error Message

```
User: "I'm getting 'module not found' error"

Agent:
  [WebSearch] query="Go module not found error solution"
  
  Search results for: Go module not found error solution
  
  1. Common Go Module Errors
     https://go.dev/doc/modules/managing-dependencies#errors
     The "module not found" error typically means...
  
  This error usually occurs when... Here are the solutions:
  1. Run `go mod tidy`
  2. Check your go.mod file...
```

### Example 4: Best Practices

```
User: "What are the best practices for error handling in Go?"

Agent:
  [WebSearch] query="Go error handling best practices 2024"
  
  Search results for: Go error handling best practices 2024
  
  1. Effective Go - Error Handling
     https://go.dev/doc/effective_go#errors
     
  2. Error Handling in Go - Best Practices
     https://earthly.dev/blog/golang-errors/
  
  Based on current best practices, here's how to handle errors...
```

## Tool Schema

```json
{
  "name": "WebSearch",
  "description": "Search the web for information...",
  "input_schema": {
    "type": "object",
    "properties": {
      "query": {
        "type": "string",
        "description": "The search query. Be specific and include relevant keywords."
      },
      "num_results": {
        "type": "number",
        "description": "Number of results to return (default: 5, max: 10)"
      }
    },
    "required": ["query"]
  }
}
```

## Output Format

```
Search results for: <query>

1. <Title>
   <URL>
   <Description>

2. <Title>
   <URL>
   <Description>

...

Found N result(s). Use the URLs to get more detailed information if needed.
```

## Environment Variables

```bash
# Search provider (optional, default: duckduckgo)
SEARCH_PROVIDER=duckduckgo  # or "brave"

# Brave API key (required if using Brave)
BRAVE_API_KEY=your_key_here
```

## Setup Guide

### Getting a Brave Search API Key

1. **Visit** https://brave.com/search/api/
2. **Sign up** for a free account
3. **Create an API key** in your dashboard
4. **Copy the key** and set it in your environment

```bash
# Add to .env file
echo "SEARCH_PROVIDER=brave" >> .env
echo "BRAVE_API_KEY=your_key_here" >> .env

# Or export in shell
export SEARCH_PROVIDER=brave
export BRAVE_API_KEY=your_key_here
```

### Free Tier Limits

- **Brave Search API**: 2,000 queries/month free
- **DuckDuckGo**: Unlimited (but limited results)

## Implementation Details

### Search Providers

#### DuckDuckGo
- Uses DuckDuckGo Instant Answer API
- Returns abstract and related topics
- No authentication required
- Good for quick lookups and definitions

#### Brave
- Uses Brave Search API v1
- Returns full web search results
- Requires API key
- Better for comprehensive searches

### Rate Limiting

The tool doesn't implement client-side rate limiting. Be mindful of:
- Brave free tier: 2,000 queries/month
- DuckDuckGo: No official limit, but be respectful

### Error Handling

The tool handles:
- Missing API key (returns helpful error message)
- Network failures (timeout after 30s)
- API errors (returns error details)
- No results (returns friendly message)

## Common Use Cases

### Documentation Lookup
```
"What are the parameters for the Anthropic Messages API?"
"How do I use lipgloss borders?"
"What's the syntax for Go generics?"
```

### Current Information
```
"What's the latest version of Node.js?"
"When was Python 3.12 released?"
"What are the current rate limits for OpenAI API?"
```

### Error Research
```
"What causes 'context deadline exceeded' in Go?"
"How to fix 'EADDRINUSE' error?"
"Why am I getting 'permission denied' on Linux?"
```

### Best Practices
```
"What are Go best practices for 2024?"
"How should I structure a REST API in Go?"
"What's the recommended way to handle database connections?"
```

### API References
```
"Anthropic API streaming parameters"
"GitHub API rate limits"
"Docker API examples"
```

## Tips for Effective Searches

### Good Query Examples
- ✅ "Go 1.22 context usage patterns"
- ✅ "Anthropic Claude API streaming examples"
- ✅ "Bubble Tea custom key bindings tutorial"

### Poor Query Examples
- ❌ "how to code" (too vague)
- ❌ "error" (not specific enough)
- ❌ "go" (too general)

### Query Optimization
1. **Include specific terms**: "Bubble Tea" not just "tea"
2. **Add context**: "Go error handling" not just "error handling"
3. **Include version numbers**: "Go 1.22" not just "Go"
4. **Use technical terms**: "API rate limit" not "request limit"

## Testing

### Manual Test (DuckDuckGo)
```bash
# Start server
just dev-server

# In CLI
> What's the latest version of Go?

# Agent should use WebSearch
[WebSearch] query="Go programming language latest version"
```

### Manual Test (Brave)
```bash
# Set up Brave
export SEARCH_PROVIDER=brave
export BRAVE_API_KEY=your_key

# Start server
just dev-server

# In CLI
> Search for Bubble Tea documentation

# Agent should use WebSearch with Brave
[WebSearch] query="Bubble Tea documentation"
```

## Troubleshooting

### "BRAVE_API_KEY not set"
```
Solution: Get API key from https://brave.com/search/api/
Set: export BRAVE_API_KEY=your_key
```

### "No results from DuckDuckGo"
```
Solution: Try Brave Search API for better results
Or: Make your query more specific
```

### "Request timeout"
```
Solution: Check internet connection
Or: Query is too complex, try simpler terms
```

### "Invalid BRAVE_API_KEY"
```
Solution: Verify key is correct
Check: Key has no extra spaces or quotes
```

## Privacy & Security

- **DuckDuckGo**: Privacy-focused, no tracking
- **Brave**: Privacy-focused, no personal data logged
- **No data stored**: Search queries not logged by Forge
- **HTTPS**: All API requests encrypted

## Cost

- **DuckDuckGo**: Free, unlimited
- **Brave Free Tier**: 2,000 queries/month ($0)
- **Brave Paid Tier**: 2M queries/month ($500)

For typical agent usage, free tier is sufficient.

## Future Enhancements

Possible improvements:
1. **Cache results** - Avoid duplicate searches
2. **More providers** - Google, Bing, etc.
3. **Page content fetch** - Get full page text
4. **Image search** - Return image URLs
5. **Search filters** - Date ranges, site-specific
6. **Query suggestions** - Help refine searches

## Comparison with Other Tools

### WebSearch vs Bash + curl
- ✅ WebSearch: Structured results, easier to parse
- ✅ WebSearch: Built-in error handling
- ✅ WebSearch: No manual URL construction
- ❌ Bash + curl: More flexible for custom APIs

### WebSearch vs Read
- ✅ WebSearch: For unknown URLs/information
- ✅ Read: For known files in workspace
- ❌ WebSearch: Can't read full page content (URLs only)

### When to Use Each
- **WebSearch**: "What's the latest version?"
- **Bash + curl**: Fetch specific API endpoint
- **Read**: Read local files
- **Grep**: Search local files

## Examples in Action

### Complete Workflow
```
User: "I'm getting an error: 'context deadline exceeded'. 
       Help me debug and fix it."

Agent:
  [WebSearch] query="Go context deadline exceeded causes solutions"
  
  Search results show common causes:
  1. Timeout too short
  2. Slow operations
  3. Missing context propagation
  
  Let me check your code...
  
  [Read] server.go
  
  I see the issue. Your timeout is 1 second but the operation
  takes longer. Here's the fix:
  
  [Edit] server.go
  Change: ctx, cancel := context.WithTimeout(ctx, 1*time.Second)
  To:     ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
  
  [Bash] go test ./...
  
  Tests pass! The issue was the timeout being too short.
```

## Documentation

- Tool implementation: `internal/tools/websearch.go`
- This guide: `docs/WEBSEARCH_TOOL.md`
- API reference: See tool schema above
