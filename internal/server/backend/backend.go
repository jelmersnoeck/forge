// Package backend manages agent processes behind different execution backends.
package backend

import "context"

// Backend manages agent processes.
type Backend interface {
	// EnsureAgent starts an agent for the session if one isn't running.
	// Returns the "host:port" address of the agent's HTTP server.
	EnsureAgent(ctx context.Context, sessionID string, opts AgentOptions) (string, error)
	// StopAgent stops the agent for the session.
	StopAgent(ctx context.Context, sessionID string) error
	// AgentAddress returns the address of a running agent, or "" if not running.
	AgentAddress(sessionID string) string
	// Close stops all agents and cleans up.
	Close() error
}

// AgentOptions configures agent startup.
type AgentOptions struct {
	CWD         string
	SessionsDir string
}
