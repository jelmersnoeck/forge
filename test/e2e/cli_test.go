//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCLIDirectMode tests the CLI in direct mode (CLI spawns agent subprocess).
func TestCLIDirectMode(t *testing.T) {
	r := require.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build forge binary
	forgeBin := buildForgeBinary(t)

	// Create a temp directory for the session
	workspace := t.TempDir()

	// Create a script that sends a simple command and exits
	// We'll use the CLI's ability to read from stdin
	inputScript := `exit
`

	cmd := exec.CommandContext(ctx, forgeBin)
	cmd.Dir = workspace
	cmd.Stdin = strings.NewReader(inputScript)
	cmd.Env = append(os.Environ(), "ANTHROPIC_API_KEY=sk-test-dummy")

	output, err := cmd.CombinedOutput()
	t.Logf("CLI output:\n%s", string(output))

	// We expect the CLI to start, potentially show a welcome message, and exit cleanly
	// Since we're sending "exit" immediately, it should not error
	r.NoError(err, "CLI should exit cleanly")
}

// TestCLIServerMode tests the CLI connecting to a forge server.
func TestCLIServerMode(t *testing.T) {
	r := require.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Build forge binary
	forgeBin := buildForgeBinary(t)

	workspace := t.TempDir()
	sessionsDir := t.TempDir()

	// Start server in daemon mode
	serverCmd := exec.CommandContext(ctx, forgeBin, "server",
		"-workspace-dir", workspace,
		"-sessions-dir", sessionsDir,
		"-port", "0", // random port
	)
	serverCmd.Env = append(os.Environ(),
		"ANTHROPIC_API_KEY=sk-test-dummy",
		"FORGE_BIN="+forgeBin,
	)

	// Capture server stdout to get the port
	stdout, err := serverCmd.StdoutPipe()
	r.NoError(err)
	stderr, err := serverCmd.StderrPipe()
	r.NoError(err)

	r.NoError(serverCmd.Start())
	defer serverCmd.Process.Kill()

	// Read the port from server output
	scanner := bufio.NewScanner(stdout)
	stderrScanner := bufio.NewScanner(stderr)
	var serverPort string

	// Combine stdout and stderr scanning
	go func() {
		for stderrScanner.Scan() {
			line := stderrScanner.Text()
			t.Logf("server stderr: %s", line)
		}
	}()

	deadline := time.After(10 * time.Second)
portLoop:
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for server to start")
		default:
		}

		if !scanner.Scan() {
			break
		}
		line := scanner.Text()
		t.Logf("server stdout: %s", line)

		// Look for the port in output like "Server listening on :3000"
		if strings.Contains(line, "listening on") || strings.Contains(line, "port") {
			parts := strings.Split(line, ":")
			if len(parts) > 1 {
				serverPort = strings.TrimSpace(parts[len(parts)-1])
				break portLoop
			}
		}
	}

	if serverPort == "" {
		t.Fatal("could not determine server port")
	}

	serverURL := fmt.Sprintf("http://localhost:%s", serverPort)
	t.Logf("Server started on %s", serverURL)

	// Now run the CLI in server mode
	inputScript := `exit
`

	cliCmd := exec.CommandContext(ctx, forgeBin, "--server", serverURL)
	cliCmd.Dir = workspace
	cliCmd.Stdin = strings.NewReader(inputScript)
	cliCmd.Env = append(os.Environ(), "ANTHROPIC_API_KEY=sk-test-dummy")

	output, err := cliCmd.CombinedOutput()
	t.Logf("CLI output:\n%s", string(output))

	// CLI should connect to server and exit cleanly
	r.NoError(err, "CLI should exit cleanly when connected to server")
}

// TestSessionPersistence tests that sessions are persisted and can be resumed.
func TestSessionPersistence(t *testing.T) {
	r := require.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	forgeBin := buildForgeBinary(t)
	workspace := t.TempDir()
	sessionsDir := t.TempDir()

	// Start server
	serverCmd := exec.CommandContext(ctx, forgeBin, "server",
		"-workspace-dir", workspace,
		"-sessions-dir", sessionsDir,
		"-port", "13579", // fixed port for this test
	)
	serverCmd.Env = append(os.Environ(),
		"ANTHROPIC_API_KEY=sk-test-dummy",
		"FORGE_BIN="+forgeBin,
	)

	r.NoError(serverCmd.Start())
	defer serverCmd.Process.Kill()

	// Give server time to start
	time.Sleep(2 * time.Second)

	serverURL := "http://localhost:13579"

	// Create a session via API
	createCmd := exec.CommandContext(ctx, "curl", "-X", "POST",
		"-H", "Content-Type: application/json",
		"-d", fmt.Sprintf(`{"cwd":"%s"}`, workspace),
		serverURL+"/sessions",
	)
	output, err := createCmd.CombinedOutput()
	r.NoError(err, "failed to create session: %s", string(output))

	var createResp struct {
		SessionID string `json:"sessionId"`
	}
	err = json.Unmarshal(output, &createResp)
	r.NoError(err)
	r.NotEmpty(createResp.SessionID)

	sessionID := createResp.SessionID
	t.Logf("Created session: %s", sessionID)

	// Verify session file was created
	sessionFile := filepath.Join(sessionsDir, sessionID+".jsonl")
	r.FileExists(sessionFile)

	// Send a message to the session
	sendCmd := exec.CommandContext(ctx, "curl", "-X", "POST",
		"-H", "Content-Type: application/json",
		"-d", `{"text":"echo test","user":"test"}`,
		serverURL+"/sessions/"+sessionID+"/messages",
	)
	output, err = sendCmd.CombinedOutput()
	r.NoError(err, "failed to send message: %s", string(output))

	// Wait a bit for the message to be processed
	time.Sleep(1 * time.Second)

	// Verify the session file has content
	content, err := os.ReadFile(sessionFile)
	r.NoError(err)
	r.NotEmpty(content, "session file should have content")

	// Now "resume" by connecting with the same session ID
	// In practice this would be done via CLI with --resume flag
	// For now we just verify the file exists and has data
	lines := strings.Split(string(content), "\n")
	var hasMessage bool
	for _, line := range lines {
		if strings.Contains(line, "echo test") {
			hasMessage = true
			break
		}
	}
	r.True(hasMessage, "session file should contain the sent message")
}

// TestCLIStatsCommand tests the stats subcommand.
func TestCLIStatsCommand(t *testing.T) {
	r := require.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	forgeBin := buildForgeBinary(t)

	// Run stats command (should work even with no data)
	cmd := exec.CommandContext(ctx, forgeBin, "stats")
	output, err := cmd.CombinedOutput()
	t.Logf("Stats output:\n%s", string(output))

	r.NoError(err, "stats command should not error")

	// Output should contain some expected strings
	outputStr := string(output)
	r.Contains(outputStr, "total", "stats should show total")
}

// TestAgentHealthCheck tests that the agent subprocess reports healthy.
func TestAgentHealthCheck(t *testing.T) {
	r := require.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	forgeBin := buildForgeBinary(t)

	// Start agent on a fixed port
	agentCmd := exec.CommandContext(ctx, forgeBin, "agent", "--port", "12345")
	agentCmd.Env = append(os.Environ(), "ANTHROPIC_API_KEY=sk-test-dummy")

	r.NoError(agentCmd.Start())
	defer agentCmd.Process.Kill()

	// Give agent time to start
	time.Sleep(2 * time.Second)

	// Hit health endpoint
	healthCmd := exec.CommandContext(ctx, "curl", "-s", "http://localhost:12345/health")
	output, err := healthCmd.CombinedOutput()
	r.NoError(err, "health check failed: %s", string(output))

	var health struct {
		Status    string `json:"status"`
		SessionID string `json:"sessionId"`
	}
	err = json.Unmarshal(output, &health)
	r.NoError(err)
	r.Equal("ok", health.Status)
}

// ── Helpers ──

func buildForgeBinary(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "forge")

	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/forge")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	require.NoError(t, err, "failed to build forge binary")

	return binPath
}
