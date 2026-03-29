package backend

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTmuxBackend_Integration(t *testing.T) {
	// Skip if tmux is not on PATH.
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found on PATH, skipping integration test")
	}

	// Skip if the agent binary isn't built.
	agentBin := "./forge-agent"
	if _, err := exec.LookPath(agentBin); err != nil {
		t.Skip("forge-agent binary not found, skipping integration test")
	}

	r := require.New(t)
	ctx := context.Background()

	b := NewTmux(agentBin, "test")
	defer b.Close()

	// Troy Barnes' enrollment session at Greendale Community College.
	sessionID := "troy-barnes-enrollment-2009"

	opts := AgentOptions{
		CWD:         t.TempDir(),
		SessionsDir: t.TempDir(),
	}

	// EnsureAgent should start the agent and return a non-empty address.
	address, err := b.EnsureAgent(ctx, sessionID, opts)
	r.NoError(err)
	r.NotEmpty(address)

	// AgentAddress should return the same address.
	r.Equal(address, b.AgentAddress(sessionID))

	// Hit the health endpoint directly.
	resp, err := http.Get(fmt.Sprintf("http://%s/health", address))
	r.NoError(err)
	defer resp.Body.Close()
	r.Equal(http.StatusOK, resp.StatusCode)

	// The shared tmux session should exist.
	err = exec.Command("tmux", "has-session", "-t", b.tmuxSessionName).Run()
	r.NoError(err, "shared tmux session should exist")

	// StopAgent should kill the window but leave the session.
	err = b.StopAgent(ctx, sessionID)
	r.NoError(err)

	// AgentAddress should return "" after stopping.
	r.Empty(b.AgentAddress(sessionID))

	// The window should be gone.
	wName := windowName(sessionID)
	out, err := exec.Command("tmux", "list-windows", "-t", b.tmuxSessionName, "-F", "#{window_name}").CombinedOutput()
	r.NoError(err)
	r.NotContains(string(out), wName)

	// Close should kill the entire session.
	err = b.Close()
	r.NoError(err)
	err = exec.Command("tmux", "has-session", "-t", b.tmuxSessionName).Run()
	r.Error(err, "tmux session should not exist after Close")
}
