//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jelmersnoeck/forge/internal/server/backend"
	"github.com/jelmersnoeck/forge/internal/server/bus"
	"github.com/jelmersnoeck/forge/internal/server/gateway"
	"github.com/jelmersnoeck/forge/internal/types"
)

// TestGatewaySessionLifecycle tests creating sessions, sending messages, and receiving events via the gateway.
func TestGatewaySessionLifecycle(t *testing.T) {
	// Skip if tmux not available
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found, skipping gateway integration test")
	}

	r := require.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Set up temporary workspace
	workspace := t.TempDir()
	sessionsDir := t.TempDir()

	// Build forge binary for backend to spawn
	forgeBin := filepath.Join(t.TempDir(), "forge")
	cmd := exec.Command("go", "build", "-o", forgeBin, "../../cmd/forge")
	cmd.Dir = workspace
	err := cmd.Run()
	if err != nil {
		// Try building from current directory
		cmd = exec.Command("go", "build", "-o", forgeBin, "./cmd/forge")
		r.NoError(cmd.Run(), "failed to build forge binary for test")
	}

	// Create backend and bus
	b := backend.NewTmux(forgeBin, "forge-test", workspace)
	defer b.Close()

	eventBus := bus.New()
	defer eventBus.Close()

	// Create gateway server
	gw := gateway.New(eventBus, b, sessionsDir)
	srv := httptest.NewServer(gw.Handler())
	defer srv.Close()

	// ── Test: Create session ──
	createReq := map[string]any{
		"cwd": workspace,
		"metadata": map[string]string{
			"user":   "Troy Barnes",
			"source": "greendale",
		},
	}
	body, err := json.Marshal(createReq)
	r.NoError(err)

	resp, err := http.Post(srv.URL+"/sessions", "application/json", bytes.NewReader(body))
	r.NoError(err)
	defer resp.Body.Close()
	r.Equal(http.StatusCreated, resp.StatusCode)

	var createResp struct {
		SessionID string `json:"sessionId"`
	}
	err = json.NewDecoder(resp.Body).Decode(&createResp)
	r.NoError(err)
	r.NotEmpty(createResp.SessionID)

	sessionID := createResp.SessionID

	// ── Test: Get session info ──
	resp, err = http.Get(srv.URL + "/sessions/" + sessionID)
	r.NoError(err)
	defer resp.Body.Close()
	r.Equal(http.StatusOK, resp.StatusCode)

	var sessionInfo struct {
		ID       string                 `json:"id"`
		Metadata map[string]interface{} `json:"metadata"`
	}
	err = json.NewDecoder(resp.Body).Decode(&sessionInfo)
	r.NoError(err)
	r.Equal(sessionID, sessionInfo.ID)

	// ── Test: Send message ──
	msg := types.InboundMessage{
		Text:      "Echo 'Hello Greendale'",
		User:      "Troy Barnes",
		Source:    "greendale",
		SessionID: sessionID,
	}
	msgBytes, err := json.Marshal(msg)
	r.NoError(err)

	resp, err = http.Post(
		srv.URL+"/sessions/"+sessionID+"/messages",
		"application/json",
		bytes.NewReader(msgBytes),
	)
	r.NoError(err)
	defer resp.Body.Close()
	r.Equal(http.StatusAccepted, resp.StatusCode)

	// ── Test: Subscribe to events ──
	// This would require running the SSE subscription in a goroutine
	// and verifying events arrive, but for simplicity we'll just check
	// the endpoint is accessible.
	resp, err = http.Get(srv.URL + "/sessions/" + sessionID + "/events")
	r.NoError(err)
	defer resp.Body.Close()
	r.Equal(http.StatusOK, resp.StatusCode)
	r.Equal("text/event-stream", resp.Header.Get("Content-Type"))

	// Give the agent a moment to start
	time.Sleep(500 * time.Millisecond)

	// ── Cleanup: Stop agent ──
	err = b.StopAgent(ctx, sessionID)
	r.NoError(err)
}

// TestGatewayErrorHandling tests error cases for the gateway.
func TestGatewayErrorHandling(t *testing.T) {
	r := require.New(t)

	workspace := t.TempDir()
	sessionsDir := t.TempDir()
	forgeBin := "forge" // doesn't need to exist for these error tests

	b := backend.NewTmux(forgeBin, "forge-test-err", workspace)
	defer b.Close()

	eventBus := bus.New()
	defer eventBus.Close()

	gw := gateway.New(eventBus, b, sessionsDir)
	srv := httptest.NewServer(gw.Handler())
	defer srv.Close()

	tests := map[string]struct {
		method   string
		path     string
		body     string
		wantCode int
	}{
		"get non-existent session": {
			method:   http.MethodGet,
			path:     "/sessions/does-not-exist",
			wantCode: http.StatusNotFound,
		},
		"send message to non-existent session": {
			method:   http.MethodPost,
			path:     "/sessions/does-not-exist/messages",
			body:     `{"text":"test"}`,
			wantCode: http.StatusNotFound,
		},
		"create session with invalid JSON": {
			method:   http.MethodPost,
			path:     "/sessions",
			body:     `{invalid json`,
			wantCode: http.StatusBadRequest,
		},
		"send message with empty text": {
			method:   http.MethodPost,
			path:     "/sessions/test/messages",
			body:     `{"text":""}`,
			wantCode: http.StatusBadRequest,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var body *bytes.Reader
			if tc.body != "" {
				body = bytes.NewReader([]byte(tc.body))
			}

			req, err := http.NewRequest(tc.method, srv.URL+tc.path, body)
			r.NoError(err)
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}

			resp, err := http.DefaultClient.Do(req)
			r.NoError(err)
			defer resp.Body.Close()
			r.Equal(tc.wantCode, resp.StatusCode)
		})
	}
}

// TestBackendWorktreeIsolation verifies that git worktrees are created and cleaned up correctly.
func TestBackendWorktreeIsolation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found, skipping worktree test")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found, skipping worktree test")
	}

	r := require.New(t)
	ctx := context.Background()

	// Create a git repo
	workspace := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = workspace
	r.NoError(cmd.Run())

	// Initial commit
	testFile := filepath.Join(workspace, "README.md")
	r.NoError(os.WriteFile(testFile, []byte("# Forge Test\n"), 0o644))
	cmd = exec.Command("git", "add", "README.md")
	cmd.Dir = workspace
	r.NoError(cmd.Run())
	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = workspace
	r.NoError(cmd.Run())

	// Build forge binary
	forgeBin := filepath.Join(t.TempDir(), "forge")
	cmd = exec.Command("go", "build", "-o", forgeBin, "./cmd/forge")
	r.NoError(cmd.Run())

	b := backend.NewTmux(forgeBin, "worktree-test", workspace)
	defer b.Close()

	sessionID := "abed-nadir-film-studies"
	opts := backend.AgentOptions{
		CWD:         workspace,
		SessionsDir: t.TempDir(),
	}

	// Start agent (should create worktree)
	address, err := b.EnsureAgent(ctx, sessionID, opts)
	r.NoError(err)
	r.NotEmpty(address)

	// Verify worktree exists
	worktreePath := filepath.Join(filepath.Dir(workspace), "worktrees", sessionID)
	r.DirExists(worktreePath)

	// Verify README exists in worktree
	r.FileExists(filepath.Join(worktreePath, "README.md"))

	// Stop agent (should clean up worktree)
	err = b.StopAgent(ctx, sessionID)
	r.NoError(err)

	// Verify worktree is gone
	r.NoDirExists(worktreePath)

	// Verify branch is deleted
	cmd = exec.Command("git", "branch", "--list", "jelmer/"+sessionID)
	cmd.Dir = workspace
	out, err := cmd.Output()
	r.NoError(err)
	r.Empty(string(out))
}
