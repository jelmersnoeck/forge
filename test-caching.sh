#!/bin/bash
# Test script for cache optimization

set -e

echo "=== Building agent ==="
just build-agent

echo ""
echo "=== Starting agent ==="
./forge-agent --port 8080 &
AGENT_PID=$!

# Wait for agent to start
sleep 2

echo ""
echo "=== Test 1: First message (should see cache_creation_tokens) ==="
curl -s -X POST http://localhost:8080/messages \
  -H "Content-Type: application/json" \
  -d '{"sessionId":"test-cache","text":"Hello, what is the working directory?"}' | \
  grep -E "cache_creation|cache_read|inputTokens" | head -10

sleep 2

echo ""
echo "=== Test 2: Second message (should see cache_read_tokens) ==="
curl -s -X POST http://localhost:8080/messages \
  -H "Content-Type: application/json" \
  -d '{"sessionId":"test-cache","text":"List the files in the current directory"}' | \
  grep -E "cache_creation|cache_read|inputTokens" | head -10

sleep 2

echo ""
echo "=== Test 3: Third message (should still see cache_read_tokens) ==="
curl -s -X POST http://localhost:8080/messages \
  -H "Content-Type: application/json" \
  -d '{"sessionId":"test-cache","text":"What files did you just list?"}' | \
  grep -E "cache_creation|cache_read|inputTokens|CACHE BREAK" | head -10

echo ""
echo "=== Stopping agent ==="
kill $AGENT_PID 2>/dev/null || true

echo ""
echo "=== Test complete! ==="
echo "Look for:"
echo "  - cache_creation_tokens > 0 on first call"
echo "  - cache_read_tokens > 0 on subsequent calls"
echo "  - NO 'CACHE BREAK' warnings (unless system changed)"
