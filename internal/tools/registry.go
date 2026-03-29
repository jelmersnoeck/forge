// Package tools implements the tool registry and built-in tools.
package tools

import (
	"fmt"
	"sync"

	"github.com/jelmersnoeck/forge/internal/types"
)

// Registry holds registered tools and dispatches execution.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]types.ToolDefinition
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]types.ToolDefinition),
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
	return schemas
}

// Execute runs a tool by name with the given input.
func (r *Registry) Execute(name string, input map[string]any, ctx types.ToolContext) (types.ToolResult, error) {
	def, ok := r.Get(name)
	if !ok {
		return types.ToolResult{}, fmt.Errorf("tool not found: %s", name)
	}
	return def.Handler(input, ctx)
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
	return r
}
