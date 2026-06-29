# Learnings - 2026-06-29 10:03

- internal/runtime/tokens/Compact must stay pure (no LLM/network I/O) — the conversation loop (internal/runtime/loop/loop.go) orchestrates LLM-backed summarization by calling tokens.DropSet -> tokens.Summarize -> tokens.CompactWithSummary. Keeping Compact pure is what lets tokens_test.go run with no provider.
- tool_result content blocks use types.ToolResultContent (Type/Text), NOT a 'ChatContentResult' type — the latter does not exist. EstimateMessage in tokens.go iterates block.Content as []ToolResultContent.
- When adding a summary-aware compaction variant, gate the new behavior behind an empty-string summary so the no-summary path stays byte-identical to the legacy boundary marker ('[Context note: N earlier messages were removed...]'). Existing tokens_test.go asserts on the substring 'earlier messages were removed'.
