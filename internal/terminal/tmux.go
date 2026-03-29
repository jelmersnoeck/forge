package terminal

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// TmuxTerminal implements Terminal using the tmux CLI.
type TmuxTerminal struct {
	mu       sync.Mutex
	sessions map[string]struct{}
}

// NewTmux creates a new TmuxTerminal.
func NewTmux() *TmuxTerminal {
	return &TmuxTerminal{
		sessions: make(map[string]struct{}),
	}
}

func (t *TmuxTerminal) CreateSession(ctx context.Context, name string) error {
	if err := tmuxRun(ctx, "new-session", "-d", "-s", name, "-x", "200", "-y", "50"); err != nil {
		return err
	}
	t.mu.Lock()
	t.sessions[name] = struct{}{}
	t.mu.Unlock()
	return nil
}

func (t *TmuxTerminal) DestroySession(ctx context.Context, name string) error {
	t.mu.Lock()
	delete(t.sessions, name)
	t.mu.Unlock()
	return tmuxRun(ctx, "kill-session", "-t", name)
}

func (t *TmuxTerminal) CreateWindow(ctx context.Context, session string, name string) (WindowID, error) {
	out, err := tmuxOutput(ctx, "new-window", "-t", session, "-n", name, "-P", "-F", "#{window_id}")
	if err != nil {
		return WindowID{}, fmt.Errorf("create window: %w", err)
	}
	return WindowID{Session: session, Window: strings.TrimSpace(out)}, nil
}

func (t *TmuxTerminal) CreatePane(ctx context.Context, window WindowID, title string) (PaneID, error) {
	target := window.Session + ":" + window.Window
	out, err := tmuxOutput(ctx, "split-window", "-t", target, "-P", "-F", "#{pane_id}")
	if err != nil {
		return PaneID{}, fmt.Errorf("create pane: %w", err)
	}

	paneID := PaneID{Window: window, Pane: strings.TrimSpace(out)}

	// Re-tile so panes are evenly distributed
	_ = tmuxRun(ctx, "select-layout", "-t", target, "tiled")

	return paneID, nil
}

func (t *TmuxTerminal) RunInPane(ctx context.Context, pane PaneID, command string, cwd string) (string, int, error) {
	channel := fmt.Sprintf("forge-done-%d", time.Now().UnixNano())

	// Send the command followed by a tmux wait-for signal
	fullCmd := fmt.Sprintf("cd %s && %s ; tmux wait-for -S %s", shellQuote(cwd), command, channel)
	if err := tmuxRun(ctx, "send-keys", "-t", pane.Pane, fullCmd, "Enter"); err != nil {
		return "", -1, fmt.Errorf("send command: %w", err)
	}

	// Block until the command completes
	if err := tmuxRun(ctx, "wait-for", channel); err != nil {
		return "", -1, fmt.Errorf("wait-for: %w", err)
	}

	// Capture the pane output
	out, err := tmuxOutput(ctx, "capture-pane", "-t", pane.Pane, "-p", "-S", "-")
	if err != nil {
		return "", -1, fmt.Errorf("capture pane: %w", err)
	}

	// Strip trailing empty lines
	return strings.TrimRight(out, "\n \t"), 0, nil
}

func (t *TmuxTerminal) ClosePane(ctx context.Context, pane PaneID) error {
	return tmuxRun(ctx, "kill-pane", "-t", pane.Pane)
}

// Close destroys all tracked tmux sessions. Call on server shutdown.
func (t *TmuxTerminal) Close() {
	t.mu.Lock()
	names := make([]string, 0, len(t.sessions))
	for name := range t.sessions {
		names = append(names, name)
	}
	t.sessions = make(map[string]struct{})
	t.mu.Unlock()

	ctx := context.Background()
	for _, name := range names {
		_ = tmuxRun(ctx, "kill-session", "-t", name)
	}
}

func tmuxRun(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tmux %s: %w: %s", args[0], err, stderr.String())
	}
	return nil
}

func tmuxOutput(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", args[0], err, stderr.String())
	}
	return stdout.String(), nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
