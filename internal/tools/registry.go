// Package tools implements the tool registry and built-in tools.
package tools

import (
	"cmp"
	"fmt"
	"slices"
	"sync"

	"github.com/jelmersnoeck/forge/internal/types"
)

// MaxResultChars is the default cap on tool result text size.
// Results exceeding this get head+tail truncated. ~7500 tokens at 4 bytes/token.
const MaxResultChars = 30_000

// Registry holds registered tools and dispatches execution.
type Registry struct {
	mu             sync.RWMutex
	tools          map[string]types.ToolDefinition
	maxResultChars int

	// permission policy. nil allow+deny => no enforcement (everything permitted).
	// "*" in allow means all tools permitted. deny always overrides allow.
	allow map[string]bool
	deny  map[string]bool
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools:          make(map[string]types.ToolDefinition),
		maxResultChars: MaxResultChars,
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(def types.ToolDefinition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[def.Name] = def
}

// Get retrieves a tool by name.
func (r *Registry) Get(name string) (types.ToolDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.tools[name]
	return def, ok
}

// IsReadOnly returns true if the named tool is marked read-only.
// Returns false for unknown tools.
func (r *Registry) IsReadOnly(name string) bool {
	def, ok := r.Get(name)
	return ok && def.ReadOnly
}

// All returns all registered tool definitions in deterministic name order.
func (r *Registry) All() []types.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]types.ToolDefinition, 0, len(r.tools))
	for _, def := range r.tools {
		defs = append(defs, def)
	}
	slices.SortFunc(defs, func(a, b types.ToolDefinition) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return defs
}

// Schemas returns schemas for all registered tools in deterministic name order.
// Only the last tool gets cache_control — a single breakpoint caches the
// entire tool list. Anthropic's API allows at most 4 cache_control blocks
// across all system + tool blocks combined.
//
// Deterministic ordering is critical: Go map iteration is random, so without
// sorting, the serialized tool list changes every turn, busting the Anthropic
// prompt cache (~10-20% hit rate). With sorting, the prefix is byte-identical
// across turns and the cache stays warm.
func (r *Registry) Schemas() []types.ToolSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()
	schemas := make([]types.ToolSchema, 0, len(r.tools))
	for _, def := range r.tools {
		schemas = append(schemas, types.ToolSchema{
			Name:        def.Name,
			Description: def.Description,
			InputSchema: def.InputSchema,
		})
	}

	slices.SortFunc(schemas, func(a, b types.ToolSchema) int {
		return cmp.Compare(a.Name, b.Name)
	})

	// Single cache breakpoint on the last tool covers all tools
	if len(schemas) > 0 {
		schemas[len(schemas)-1].CacheControl = &types.CacheControl{
			Type: "ephemeral",
			TTL:  "1h",
		}
	}

	return schemas
}

// Execute runs a tool by name with the given input.
// Results exceeding MaxResultChars are truncated (head+tail).
func (r *Registry) Execute(name string, input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	def, ok := r.Get(name)
	if !ok {
		return types.ToolResult{}, fmt.Errorf("tool not found: %s", name)
	}

	// Defense in depth: enforce the permission policy before running the handler.
	// Schema-level filtering (Filtered/WithPermissions) hides tools from the LLM,
	// but resumed history, hallucinated names, or the MCP gateway can still reach
	// here. A denial round-trips as an error ToolResult (not a Go error) so the
	// conversation loop continues and the model can adapt.
	//
	// Extension point (issue #256 Phase 3): granular policies like "Bash:read-only"
	// or "Write:*.md" would refine permitted() to inspect input, not just the name.
	if !r.permitted(name) {
		return types.ToolResult{
			Content: []types.ToolResultContent{
				{Type: "text", Text: fmt.Sprintf("Tool '%s' denied by permission policy", name)},
			},
			IsError: true,
		}, nil
	}

	result, err := def.Handler(input, ctx)
	if err != nil {
		return result, err
	}

	// Don't truncate errors — they're usually short and always important.
	if result.IsError {
		return result, nil
	}

	r.truncateResult(&result)
	return result, nil
}

// truncateResult caps text content blocks that exceed maxResultChars.
// Keeps 40% from the head and 40% from the tail with a marker in between.
func (r *Registry) truncateResult(result *types.ToolResult) {
	for i, block := range result.Content {
		switch block.Type {
		case "text":
			if len(block.Text) <= r.maxResultChars {
				continue
			}
			headSize := r.maxResultChars * 2 / 5
			tailSize := r.maxResultChars * 2 / 5
			omitted := len(block.Text) - headSize - tailSize

			result.Content[i].Text = block.Text[:headSize] +
				fmt.Sprintf("\n\n... [truncated: %d characters omitted] ...\n\n", omitted) +
				block.Text[len(block.Text)-tailSize:]

		case "image":
			// Images are passed through — the API handles them separately.
		}
	}
}

// Filtered creates a new Registry containing only tools matching the constraints.
//
//   - If allowList is non-empty, only those tools are included.
//   - Any tool in denyList is excluded regardless of the allow list.
//
// The returned registry is independent — mutations don't affect the original.
func (r *Registry) Filtered(allowList, denyList []string) *Registry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	allow := make(map[string]bool, len(allowList))
	for _, name := range allowList {
		allow[name] = true
	}
	deny := make(map[string]bool, len(denyList))
	for _, name := range denyList {
		deny[name] = true
	}

	filtered := NewRegistry()
	for name, def := range r.tools {
		if deny[name] {
			continue
		}
		if len(allow) > 0 && !allow[name] {
			continue
		}
		filtered.tools[name] = def
	}
	return filtered
}

// WithPermissions returns a registry that BOTH omits disallowed tools from its
// schemas (like Filtered) AND enforces the allow/deny policy in Execute.
//
// A "*" entry in allow means all tools are permitted (only deny restricts).
// Deny always wins over allow. An empty allow with empty deny enforces nothing.
//
// Schema hiding uses the same rules as Filtered except that "*" in allow is
// treated as "include everything" so the LLM still sees all permitted tools.
func (r *Registry) WithPermissions(allow, deny []string) *Registry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	allowSet := make(map[string]bool, len(allow))
	allowAll := false
	for _, name := range allow {
		if name == "*" {
			allowAll = true
			continue
		}
		allowSet[name] = true
	}
	denySet := make(map[string]bool, len(deny))
	for _, name := range deny {
		denySet[name] = true
	}

	out := NewRegistry()
	out.allow = allowSet
	out.deny = denySet
	if allowAll {
		// Preserve the "*" marker so permitted() permits unlisted tools.
		out.allow["*"] = true
	}

	for name, def := range r.tools {
		if denySet[name] {
			continue
		}
		if !allowAll && len(allowSet) > 0 && !allowSet[name] {
			continue
		}
		out.tools[name] = def
	}
	return out
}

// permitted reports whether name may execute under this registry's policy.
// A nil/empty policy (no allow and no deny) permits everything — this keeps the
// main interactive registry unrestricted unless a policy is explicitly applied.
func (r *Registry) permitted(name string) bool {
	if len(r.allow) == 0 && len(r.deny) == 0 {
		return true
	}
	if r.deny[name] {
		return false
	}
	if r.allow["*"] {
		return true
	}
	if len(r.allow) > 0 {
		return r.allow[name]
	}
	// deny-only policy: permit anything not denied.
	return true
}

// NewDefaultRegistry creates a registry with all built-in tools.
func NewDefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(ReadTool())
	r.Register(WriteTool())
	r.Register(EditTool())
	r.Register(BashTool())
	r.Register(GlobTool())
	r.Register(GrepTool())
	r.Register(QueueImmediateTool)
	r.Register(QueueOnCompleteTool)
	r.Register(WebSearchTool())
	r.Register(ReflectTool())
	// Background task tools
	r.Register(TaskCreateTool())
	r.Register(TaskGetTool())
	r.Register(TaskListTool())
	r.Register(TaskStopTool())
	r.Register(TaskOutputTool())
	// Sub-agent tools
	r.Register(AgentTool())
	r.Register(AgentGetTool())
	r.Register(AgentListTool())
	r.Register(AgentStopTool())
	// MCP gateway (lazy tool loading — one tool instead of N*25)
	r.Register(UseMCPTool())
	return r
}
