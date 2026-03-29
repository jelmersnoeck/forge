package terminal

import (
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTmuxTerminal_Integration(t *testing.T) {
	// Skip if tmux is not on PATH
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found on PATH, skipping integration test")
	}

	r := require.New(t)
	ctx := context.Background()
	term := NewTmux()

	sessionName := "forge-test-greendale"

	// Clean up in case a previous test run left this around
	_ = term.DestroySession(ctx, sessionName)

	// Create session
	err := term.CreateSession(ctx, sessionName)
	r.NoError(err)

	defer func() {
		_ = term.DestroySession(ctx, sessionName)
	}()

	// Create window
	win, err := term.CreateWindow(ctx, sessionName, "study-room")
	r.NoError(err)
	r.Equal(sessionName, win.Session)
	r.NotEmpty(win.Window)

	// Create pane
	pane, err := term.CreatePane(ctx, win, "troy")
	r.NoError(err)
	r.NotEmpty(pane.Pane)

	// Run command in pane
	dir := t.TempDir()
	output, exitCode, err := term.RunInPane(ctx, pane, "echo 'streets ahead'", dir)
	r.NoError(err)
	r.Equal(0, exitCode)
	r.Contains(output, "streets ahead")

	// Close pane
	err = term.ClosePane(ctx, pane)
	r.NoError(err)

	// Destroy session
	err = term.DestroySession(ctx, sessionName)
	r.NoError(err)
}
