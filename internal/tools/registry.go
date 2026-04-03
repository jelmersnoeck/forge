// Package tools implements the tool registry and built-in tools.
package tools

import (
	"fmt"
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

// All returns all registered tool definitions.
func (r *Registry) All() []types.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]types.ToolDefinition, 0, len(r.tools))
	for _, def := range r.tools {
		defs = append(defs, def)
	}
	return defs
}

// Schemas returns schemas for all registered tools.
// Only the last tool gets cache_control — a single breakpoint caches the
// entire tool list. Anthropic's API allows at most 4 cache_control blocks
// across all system + tool blocks combined.
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
	return r
}
