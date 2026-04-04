package terminal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInProcessTerminal_FullLifecycle(t *testing.T) {
	r := require.New(t)
	ctx := context.Background()
	term := NewInProcess()

	// Create session
	err := term.CreateSession(ctx, "greendale")
	r.NoError(err)

	// Duplicate session fails
	err = term.CreateSession(ctx, "greendale")
	r.Error(err)

	// Create window
	win, err := term.CreateWindow(ctx, "greendale", "study-room-f")
	r.NoError(err)
	r.Equal("greendale", win.Session)
	r.NotEmpty(win.Window)

	// Create pane
	pane, err := term.CreatePane(ctx, win, "troy-task")
	r.NoError(err)
	r.Equal(win, pane.Window)
	r.NotEmpty(pane.Pane)

	// Run command in pane
	dir := t.TempDir()
	output, exitCode, err := term.RunInPane(ctx, pane, "echo 'cool cool cool'", dir)
	r.NoError(err)
	r.Equal(0, exitCode)
	r.Contains(output, "cool cool cool")

	// Run failing command
	_, exitCode, err = term.RunInPane(ctx, pane, "exit 42", dir)
	r.NoError(err)
	r.Equal(42, exitCode)

	// Close pane
	err = term.ClosePane(ctx, pane)
	r.NoError(err)

	// Close pane again fails
	err = term.ClosePane(ctx, pane)
	r.Error(err)

	// Destroy session
	err = term.DestroySession(ctx, "greendale")
	r.NoError(err)

	// Destroy again fails
	err = term.DestroySession(ctx, "greendale")
	r.Error(err)
}

func TestInProcessTerminal_WindowOnMissingSession(t *testing.T) {
	r := require.New(t)
	ctx := context.Background()
	term := NewInProcess()

	_, err := term.CreateWindow(ctx, "city-college", "evil-study-group")
	r.Error(err)
}

func TestInProcessTerminal_PaneOnMissingWindow(t *testing.T) {
	r := require.New(t)
	ctx := context.Background()
	term := NewInProcess()

	err := term.CreateSession(ctx, "greendale")
	r.NoError(err)

	_, err = term.CreatePane(ctx, WindowID{Session: "greendale", Window: "@999"}, "abed-task")
	r.Error(err)
}

func TestInProcessTerminal_MultiplePane(t *testing.T) {
	r := require.New(t)
	ctx := context.Background()
	term := NewInProcess()

	err := term.CreateSession(ctx, "greendale")
	r.NoError(err)

	win, err := term.CreateWindow(ctx, "greendale", "study-room")
	r.NoError(err)

	pane1, err := term.CreatePane(ctx, win, "jeff")
	r.NoError(err)

	pane2, err := term.CreatePane(ctx, win, "britta")
	r.NoError(err)

	// Run commands in different panes
	dir := t.TempDir()
	out1, code1, err := term.RunInPane(ctx, pane1, "echo 'winger speech'", dir)
	r.NoError(err)
	r.Equal(0, code1)
	r.Contains(out1, "winger speech")

	out2, code2, err := term.RunInPane(ctx, pane2, "echo \"britta'd it\"", dir)
	r.NoError(err)
	r.Equal(0, code2)
	r.Contains(out2, "britta'd it")

	// Closing one pane doesn't affect the other
	err = term.ClosePane(ctx, pane1)
	r.NoError(err)

	out3, code3, err := term.RunInPane(ctx, pane2, "echo 'still here'", dir)
	r.NoError(err)
	r.Equal(0, code3)
	r.Contains(out3, "still here")
}

func TestInProcessTerminal_DestroySessionCleansUpPanes(t *testing.T) {
	r := require.New(t)
	ctx := context.Background()
	term := NewInProcess()

	err := term.CreateSession(ctx, "greendale")
	r.NoError(err)

	win, err := term.CreateWindow(ctx, "greendale", "paintball")
	r.NoError(err)

	_, err = term.CreatePane(ctx, win, "chang")
	r.NoError(err)

	// Destroying session should clean up panes
	err = term.DestroySession(ctx, "greendale")
	r.NoError(err)
}

func TestInProcessTerminal_Close(t *testing.T) {
	r := require.New(t)
	ctx := context.Background()
	term := NewInProcess()

	err := term.CreateSession(ctx, "greendale")
	r.NoError(err)
	err = term.CreateSession(ctx, "city-college")
	r.NoError(err)

	term.Close()

	// Sessions should be gone
	_, err = term.CreateWindow(ctx, "greendale", "test")
	r.Error(err)
	_, err = term.CreateWindow(ctx, "city-college", "test")
	r.Error(err)
}
