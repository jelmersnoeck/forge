// Package terminal provides an abstraction for terminal session management.
// Implementations map to tmux's hierarchy: sessions → windows → panes.
package terminal

import "context"

// WindowID identifies a window within a terminal session.
type WindowID struct {
	Session string
	Window  string
}

// PaneID identifies a pane within a window.
type PaneID struct {
	Window WindowID
	Pane   string
}

// Terminal abstracts terminal session management.
type Terminal interface {
	CreateSession(ctx context.Context, name string) error
	DestroySession(ctx context.Context, name string) error
	CreateWindow(ctx context.Context, session string, name string) (WindowID, error)
	CreatePane(ctx context.Context, window WindowID, title string) (PaneID, error)
	RunInPane(ctx context.Context, pane PaneID, command string, cwd string) (output string, exitCode int, err error)
	ClosePane(ctx context.Context, pane PaneID) error
	// Close destroys all tracked sessions. Call on server/process shutdown.
	Close()
}
