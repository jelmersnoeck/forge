package mcp

import (
	"context"
	"fmt"
	"sync"
)

// Store holds MCP clients and their cached tool catalogs, providing
// lazy access to tool schemas without registering them with the main
// tool registry. This keeps MCP tool definitions out of every LLM
// API call and only surfaces them when explicitly requested.
//
//	┌─────────────┐         ┌────────────────┐
//	│  LLM sees   │         │   MCPStore     │
//	│  UseMCPTool │──call──▶│  ┌───────────┐ │
//	│  (~300 tok) │         │  │ datadog   │ │
//	└─────────────┘         │  │  25 tools │ │
//	                        │  └───────────┘ │
//	                        │  ┌───────────┐ │
//	                        │  │ sentry    │ │
//	                        │  │  12 tools │ │
//	                        │  └───────────┘ │
//	                        └────────────────┘
type Store struct {
	mu      sync.RWMutex
	servers map[string]*serverEntry
}

type serverEntry struct {
	client *Client
	tools  []MCPTool // cached from ListTools
}

// NewStore creates an empty MCP store.
func NewStore() *Store {
	return &Store{
		servers: make(map[string]*serverEntry),
	}
}

// Add registers a connected MCP client and caches its tools.
func (s *Store) Add(client *Client, tools []MCPTool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.servers[client.ServerName()] = &serverEntry{
		client: client,
		tools:  tools,
	}
}

// ServerInfo is a summary of a registered MCP server.
type ServerInfo struct {
	Name      string
	ToolCount int
}

// ListServers returns a summary of all registered MCP servers.
func (s *Store) ListServers() []ServerInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	infos := make([]ServerInfo, 0, len(s.servers))
	for name, entry := range s.servers {
		infos = append(infos, ServerInfo{
			Name:      name,
			ToolCount: len(entry.tools),
		})
	}
	return infos
}

// ListTools returns the cached tool catalog for a server.
func (s *Store) ListTools(serverName string) ([]MCPTool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.servers[serverName]
	if !ok {
		return nil, fmt.Errorf("unknown MCP server: %q", serverName)
	}
	return entry.tools, nil
}

// GetTool returns a single tool's full schema from a server.
func (s *Store) GetTool(serverName, toolName string) (MCPTool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.servers[serverName]
	if !ok {
		return MCPTool{}, fmt.Errorf("unknown MCP server: %q", serverName)
	}
	for _, t := range entry.tools {
		if t.Name == toolName {
			return t, nil
		}
	}
	return MCPTool{}, fmt.Errorf("tool %q not found on server %q", toolName, serverName)
}

// CallTool invokes a tool on the named MCP server.
func (s *Store) CallTool(ctx context.Context, serverName, toolName string, arguments map[string]any) (*MCPToolResult, error) {
	s.mu.RLock()
	entry, ok := s.servers[serverName]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown MCP server: %q", serverName)
	}
	return entry.client.CallTool(ctx, toolName, arguments)
}

// Clients returns all stored MCP clients (for cleanup/close).
func (s *Store) Clients() []*Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	clients := make([]*Client, 0, len(s.servers))
	for _, entry := range s.servers {
		clients = append(clients, entry.client)
	}
	return clients
}

// Empty reports whether any MCP servers are registered.
func (s *Store) Empty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.servers) == 0
}
