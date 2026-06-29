# Learnings - 2026-06-29 10:22

- forge sub-agent tool restriction was schema-only (Registry.Filtered omits denied tools from the LLM-visible schema) with NO execution-time enforcement — a denied tool reaching Registry.Execute via resumed history/hallucination/MCP gateway ran anyway. Fix: enforce inside Registry.Execute (the single chokepoint every caller routes through) and return denial as an IsError ToolResult, NOT a Go error (a Go error aborts the conversation loop; an error ToolResult lets the model adapt).
- internal/tools/registry_test.go uses gofmt-strict struct-field alignment in table-driven tests; appended structs with mixed tab/space comment alignment trip `gofmt -l`. Run `gofmt -w` on test files before claiming clean — go vet won't catch formatting.
