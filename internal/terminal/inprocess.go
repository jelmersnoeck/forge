package terminal

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sync"
)

// InProcessTerminal implements Terminal in-memory for testing and dev without tmux.
type InProcessTerminal struct {
	mu       sync.Mutex
	sessions map[string]map[string]*inProcessWindow // session → window → *window
	panes    map[string]*inProcessPane              // paneID → *pane
	nextID   int
}

type inProcessWindow struct {
	name  string
	panes []string // pane IDs
}

type inProcessPane struct {
	window WindowID
	title  string
}

// NewInProcess creates a new InProcessTerminal.
func NewInProcess() *InProcessTerminal {
	return &InProcessTerminal{
		sessions: make(map[string]map[string]*inProcessWindow),
		panes:    make(map[string]*inProcessPane),
	}
}

func (t *InProcessTerminal) CreateSession(ctx context.Context, name string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.sessions[name]; ok {
		return fmt.Errorf("session %q already exists", name)
	}
	t.sessions[name] = make(map[string]*inProcessWindow)
	return nil
}

func (t *InProcessTerminal) DestroySession(ctx context.Context, name string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	windows, ok := t.sessions[name]
	if !ok {
		return fmt.Errorf("session %q not found", name)
	}

	// Clean up all panes in all windows
	for _, w := range windows {
		for _, paneID := range w.panes {
			delete(t.panes, paneID)
		}
	}

	delete(t.sessions, name)
	return nil
}

func (t *InProcessTerminal) CreateWindow(ctx context.Context, session string, name string) (WindowID, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	windows, ok := t.sessions[session]
	if !ok {
		return WindowID{}, fmt.Errorf("session %q not found", session)
	}

	t.nextID++
	windowID := fmt.Sprintf("@%d", t.nextID)
	windows[windowID] = &inProcessWindow{name: name}

	return WindowID{Session: session, Window: windowID}, nil
}

func (t *InProcessTerminal) CreatePane(ctx context.Context, window WindowID, title string) (PaneID, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	windows, ok := t.sessions[window.Session]
	if !ok {
		return PaneID{}, fmt.Errorf("session %q not found", window.Session)
	}

	w, ok := windows[window.Window]
	if !ok {
		return PaneID{}, fmt.Errorf("window %q not found", window.Window)
	}

	t.nextID++
	paneIDStr := fmt.Sprintf("%%%d", t.nextID)

	t.panes[paneIDStr] = &inProcessPane{
		window: window,
		title:  title,
	}
	w.panes = append(w.panes, paneIDStr)

	return PaneID{Window: window, Pane: paneIDStr}, nil
}

func (t *InProcessTerminal) RunInPane(ctx context.Context, pane PaneID, command string, cwd string) (string, int, error) {
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = cwd

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	output := stdout.String()
	if stderr.Len() > 0 {
		if len(output) > 0 {
			output += "\n"
		}
		output += stderr.String()
	}

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return output, -1, err
		}
	}

	return output, exitCode, nil
}

func (t *InProcessTerminal) ClosePane(ctx context.Context, pane PaneID) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	p, ok := t.panes[pane.Pane]
	if !ok {
		return fmt.Errorf("pane %q not found", pane.Pane)
	}

	// Remove from window's pane list
	if windows, ok := t.sessions[p.window.Session]; ok {
		if w, ok := windows[p.window.Window]; ok {
			for i, id := range w.panes {
				if id == pane.Pane {
					w.panes = append(w.panes[:i], w.panes[i+1:]...)
					break
				}
			}
		}
	}

	delete(t.panes, pane.Pane)
	return nil
}

// Close destroys all tracked sessions.
func (t *InProcessTerminal) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessions = make(map[string]map[string]*inProcessWindow)
	t.panes = make(map[string]*inProcessPane)
}
