---
id: fix-classification-model-404
status: implemented
---
# Fix classification 404 on session start

## Description
Intent classification failed every session with a 404 error because both
fallback models were unavailable: one retired, one intermittently inaccessible.

## Context
- `internal/agent/phase/classify.go` — `classificationModels` list
- `internal/runtime/cost/cost.go` — `modelPricing` map
- Error path: `orchestrator.go:101` → `ClassifyIntent` → `classifyWithModel` → Anthropic API 404

## Behavior
- Classification uses `claude-haiku-4-5` (alias) as primary model.
- Falls back to `claude-haiku-4-5-20251001` (dated) if alias fails.
- Cost tracking recognizes `claude-haiku-4-5` alias with correct Haiku pricing.
- Retired `claude-3-5-haiku-20241022` is no longer attempted.

## Constraints
- Must not remove retired models from cost pricing (historical session lookups need them).
- Must keep at least 2 models in fallback list for resilience.

## Interfaces
```go
var classificationModels = []string{
    "claude-haiku-4-5",
    "claude-haiku-4-5-20251001",
}
```

## Edge Cases
- Alias `claude-haiku-4-5` resolves to a future model with different behavior → classification still returns JSON `{"intent": "..."}`, parser handles it.
- Both models fail → defaults to `IntentTask` with error logged (existing behavior, unchanged).
- API key lacks Haiku access → falls through both models, defaults to task (non-fatal).
