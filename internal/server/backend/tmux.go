package backend

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// TmuxBackend runs each agent as a window inside a single tmux session.
type TmuxBackend struct {
	mu              sync.Mutex
	agents          map[string]*agentInfo // sessionID -> info
	forgeBin        string                // absolute path to forge binary
	tmuxSessionName string                // unique tmux session name per server
	sessionReady    bool                  // whether the tmux session has been created
	worktreeMgr     *WorktreeManager      // manages git worktrees for session isolation
}

type agentInfo struct {
	address    string // "localhost:PORT"
	windowName string // tmux window name within the session
}

// NewTmux creates a TmuxBackend that launches agents using the given binary.
// The binary path is resolved to an absolute path at creation time.
// serverID is used to create a unique tmux session name so multiple servers
// don't collide.
// baseWorkspace is the default workspace directory; if it's in a git repo,
// sessions will use git worktrees for isolation.
func NewTmux(forgeBin string, serverID string, baseWorkspace string) *TmuxBackend {
	// Resolve to absolute path so tmux sessions can find it.
	if abs, err := filepath.Abs(forgeBin); err == nil {
		forgeBin = abs
	}
	// If it's a bare name (no slashes), try to find it on PATH.
	if filepath.Base(forgeBin) == forgeBin {
		if resolved, err := exec.LookPath(forgeBin); err == nil {
			forgeBin = resolved
		}
	}

	worktreeDir := filepath.Join(filepath.Dir(baseWorkspace), "worktrees")
	worktreeMgr := NewWorktreeManager(baseWorkspace, worktreeDir)

	return &TmuxBackend{
		agents:          make(map[string]*agentInfo),
		forgeBin:        forgeBin,
		tmuxSessionName: "forge-" + serverID,
		worktreeMgr:     worktreeMgr,
	}
}

// ensureSession creates the shared tmux session if it doesn't exist yet.
// Must be called with b.mu held.
func (b *TmuxBackend) ensureSession() error {
	if b.sessionReady {
		return nil
	}

	// Check if session already exists (e.g. from a previous server run).
	if err := exec.Command("tmux", "has-session", "-t", b.tmuxSessionName).Run(); err == nil {
		b.sessionReady = true
		return nil
	}

	// Create the session. It starts with a default window which we'll leave idle.
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", b.tmuxSessionName).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux new-session: %w: %s", err, out)
	}

	log.Printf("[tmux] created session %s", b.tmuxSessionName)
	b.sessionReady = true
	return nil
}

// windowName returns a stable, short tmux window name for a session ID.
func windowName(sessionID string) string {
	if len(sessionID) > 8 {
		return sessionID[:8]
	}
	return sessionID
}

// EnsureAgent starts an agent for the session if one isn't already running.
// It returns the "host:port" address of the agent's HTTP server.
func (b *TmuxBackend) EnsureAgent(ctx context.Context, sessionID string, opts AgentOptions) (string, error) {
	b.mu.Lock()
	if info, ok := b.agents[sessionID]; ok {
		b.mu.Unlock()
		return info.address, nil
	}

	if err := b.ensureSession(); err != nil {
		b.mu.Unlock()
		return "", err
	}
	b.mu.Unlock()

	// Create or reuse a git worktree for this session
	worktreePath, err := b.worktreeMgr.EnsureWorktree(sessionID)
	if err != nil {
		return "", fmt.Errorf("failed to create worktree: %w", err)
	}

	// Use the worktree path as CWD instead of opts.CWD
	cwd := worktreePath

	// Find a free port.
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return "", fmt.Errorf("find free port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	wName := windowName(sessionID)

	// Build the agent command. Use 'forge agent' subcommand. Quote paths to handle spaces.
	command := fmt.Sprintf("%q agent --port %d --cwd %q --session-id %s --sessions-dir %q",
		b.forgeBin, port, cwd, sessionID, opts.SessionsDir)

	// Create a new window inside the shared tmux session.
	cmd := exec.CommandContext(ctx, "tmux", "new-window",
		"-t", b.tmuxSessionName,
		"-n", wName,
		"-c", cwd,
		command)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("tmux new-window: %w: %s", err, out)
	}

	// Poll the health endpoint until the agent is ready.
	address := fmt.Sprintf("localhost:%d", port)
	healthURL := fmt.Sprintf("http://%s/health", address)
	client := &http.Client{Timeout: 2 * time.Second}

	var lastErr error
	for i := 0; i < 50; i++ {
		resp, err := client.Get(healthURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				b.mu.Lock()
				b.agents[sessionID] = &agentInfo{
					address:    address,
					windowName: wName,
				}
				b.mu.Unlock()
				return address, nil
			}
			lastErr = fmt.Errorf("health check returned status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Agent didn't come up; kill the window to clean up.
	_ = exec.Command("tmux", "kill-window", "-t", b.tmuxSessionName+":"+wName).Run()
	return "", fmt.Errorf("agent health check timed out after 5s: %w", lastErr)
}

// StopAgent stops the agent for the given session.
func (b *TmuxBackend) StopAgent(ctx context.Context, sessionID string) error {
	b.mu.Lock()
	info, ok := b.agents[sessionID]
	if !ok {
		b.mu.Unlock()
		return nil
	}
	delete(b.agents, sessionID)
	b.mu.Unlock()

	target := b.tmuxSessionName + ":" + info.windowName
	log.Printf("[tmux] stopping agent id=%s window=%s", sessionID, target)
	cmd := exec.CommandContext(ctx, "tmux", "kill-window", "-t", target)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux kill-window: %w: %s", err, out)
	}

	// Clean up the worktree
	if err := b.worktreeMgr.RemoveWorktree(sessionID); err != nil {
		log.Printf("[tmux] failed to remove worktree for session %s: %v", sessionID, err)
		// Non-fatal, continue
	}

	return nil
}

// AgentAddress returns the address of a running agent, or "" if not running.
func (b *TmuxBackend) AgentAddress(sessionID string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if info, ok := b.agents[sessionID]; ok {
		return info.address
	}
	return ""
}

// Close stops all running agents and kills the tmux session.
func (b *TmuxBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	log.Printf("[tmux] closing session %s (%d agents)", b.tmuxSessionName, len(b.agents))

	// Clean up worktrees for all sessions
	for sessionID := range b.agents {
		if err := b.worktreeMgr.RemoveWorktree(sessionID); err != nil {
			log.Printf("[tmux] failed to remove worktree for session %s: %v", sessionID, err)
		}
		delete(b.agents, sessionID)
	}
	b.sessionReady = false

	cmd := exec.Command("tmux", "kill-session", "-t", b.tmuxSessionName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux kill-session: %w: %s", err, out)
	}
	return nil
}
