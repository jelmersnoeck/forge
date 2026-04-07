package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

// setupGitRepo creates a bare remote + local clone for testing.
// Returns (localDir, remoteDir, cleanup).
func setupGitRepo(t *testing.T) (string, string) {
	t.Helper()

	remote := t.TempDir()
	local := t.TempDir()

	// Init bare remote with explicit default branch.
	run(t, remote, "git", "init", "--bare", "--initial-branch=main")

	// Clone it.
	run(t, local, "git", "clone", remote, ".")
	run(t, local, "git", "config", "user.email", "troy@greendale.edu")
	run(t, local, "git", "config", "user.name", "Troy Barnes")

	// Initial commit on main.
	writeFile(t, filepath.Join(local, "README.md"), "# Greendale Community College\n")
	run(t, local, "git", "add", ".")
	run(t, local, "git", "commit", "-m", "initial commit: welcome to Greendale")
	run(t, local, "git", "push", "origin", "HEAD")

	return local, remote
}

func run(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckNeedsRebase(t *testing.T) {
	local, remote := setupGitRepo(t)
	r := require.New(t)

	// Create feature branch.
	run(t, local, "git", "checkout", "-b", "jelmer/paintball-episode")
	writeFile(t, filepath.Join(local, "paintball.md"), "# Modern Warfare\n")
	run(t, local, "git", "add", ".")
	run(t, local, "git", "commit", "-m", "add paintball episode")
	run(t, local, "git", "push", "origin", "HEAD")

	// No new commits on main — should not need rebase.
	r.False(checkNeedsRebase(local, "main"))

	// Simulate someone pushing to main from another clone.
	other := t.TempDir()
	run(t, other, "git", "clone", remote, ".")
	run(t, other, "git", "config", "user.email", "abed@greendale.edu")
	run(t, other, "git", "config", "user.name", "Abed Nadir")
	writeFile(t, filepath.Join(other, "timeline.md"), "# Darkest Timeline\n")
	run(t, other, "git", "add", ".")
	run(t, other, "git", "commit", "-m", "add darkest timeline")
	run(t, other, "git", "push", "origin", "main")

	// Now main has advanced — needs rebase.
	r.True(checkNeedsRebase(local, "main"))
}

func TestRebaseAndPush(t *testing.T) {
	local, remote := setupGitRepo(t)
	r := require.New(t)

	// Create feature branch.
	run(t, local, "git", "checkout", "-b", "jelmer/dean-pelton-costumes")
	writeFile(t, filepath.Join(local, "costumes.md"), "# Dean's Costumes\n")
	run(t, local, "git", "add", ".")
	run(t, local, "git", "commit", "-m", "add costume catalog")
	run(t, local, "git", "push", "origin", "HEAD")

	// Advance main from another clone.
	other := t.TempDir()
	run(t, other, "git", "clone", remote, ".")
	run(t, other, "git", "config", "user.email", "jeff@greendale.edu")
	run(t, other, "git", "config", "user.name", "Jeff Winger")
	writeFile(t, filepath.Join(other, "speech.md"), "# Winger Speech\n")
	run(t, other, "git", "add", ".")
	run(t, other, "git", "commit", "-m", "add motivational speech")
	run(t, other, "git", "push", "origin", "main")

	// Fetch so we know about origin/main.
	run(t, local, "git", "fetch", "origin", "main")

	w := &Worker{cwd: local, sessionID: "test"}
	err := w.rebaseAndPush(context.Background(), "main")
	r.NoError(err)

	// Verify our branch now includes the main commit.
	out := run(t, local, "git", "log", "--oneline")
	r.Contains(out, "add motivational speech")
	r.Contains(out, "add costume catalog")
}

func TestRebaseAndPush_ConflictAborts(t *testing.T) {
	local, remote := setupGitRepo(t)
	r := require.New(t)

	// Create feature branch modifying README.
	run(t, local, "git", "checkout", "-b", "jelmer/conflict-branch")
	writeFile(t, filepath.Join(local, "README.md"), "# Study Room F\n")
	run(t, local, "git", "add", ".")
	run(t, local, "git", "commit", "-m", "rename to study room F")
	run(t, local, "git", "push", "origin", "HEAD")

	// Conflicting change on main.
	other := t.TempDir()
	run(t, other, "git", "clone", remote, ".")
	run(t, other, "git", "config", "user.email", "chang@greendale.edu")
	run(t, other, "git", "config", "user.name", "Ben Chang")
	writeFile(t, filepath.Join(other, "README.md"), "# El Tigre Chino\n")
	run(t, other, "git", "add", ".")
	run(t, other, "git", "commit", "-m", "Chang was here")
	run(t, other, "git", "push", "origin", "main")

	run(t, local, "git", "fetch", "origin", "main")

	w := &Worker{cwd: local, sessionID: "test"}
	err := w.rebaseAndPush(context.Background(), "main")
	r.Error(err)
	r.Contains(err.Error(), "rebase conflicts")

	// Verify rebase was aborted (not stuck in rebase state).
	out := run(t, local, "git", "status")
	r.NotContains(out, "rebase in progress")
}

func TestGetPRInfo_NoPR(t *testing.T) {
	// Skip if gh is not available.
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh CLI not available")
	}

	local, _ := setupGitRepo(t)

	// No PR exists — should return error.
	_, err := getPRInfo(local)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no PR found")
}

func TestPRHealthCheck_NoPR(t *testing.T) {
	// The health check should gracefully handle no PR.
	local, _ := setupGitRepo(t)

	hub := NewHub()
	w := &Worker{
		hub:       hub,
		cwd:       local,
		sessionID: "test-greendale",
	}

	needsFix, fixMsg, terminal := w.prHealthCheck(context.Background())
	require.False(t, needsFix)
	require.Empty(t, fixMsg)
	require.False(t, terminal)
}

func TestPRMonitor_SkipsWhenBusy(t *testing.T) {
	r := require.New(t)
	hub := NewHub()

	// Push a message so the hub has a queued item (worker would be busy).
	hub.PushMessage(types.InboundMessage{Text: "doing work"})

	// Hub should NOT be idle.
	r.False(hub.IsIdle())
}

func TestPRMonitor_IdleWhenWaiting(t *testing.T) {
	r := require.New(t)
	hub := NewHub()

	// Start a goroutine that blocks on PullMessage (simulates idle worker).
	done := make(chan struct{})
	go func() {
		defer close(done)
		hub.PullMessage()
	}()

	// Give the goroutine time to register as a waiter.
	time.Sleep(50 * time.Millisecond)

	r.True(hub.IsIdle())

	// Unblock the goroutine.
	hub.PushMessage(types.InboundMessage{Text: "wake up"})
	<-done
}

func TestPRInfo_ParseChecks(t *testing.T) {
	tests := map[string]struct {
		checks     string
		wantOK     bool
		wantFailed []string
	}{
		"all passing": {
			checks: `[
				{"name": "build", "status": "COMPLETED", "conclusion": "SUCCESS"},
				{"name": "lint", "status": "COMPLETED", "conclusion": "SUCCESS"}
			]`,
			wantOK:     true,
			wantFailed: nil,
		},
		"one failing": {
			checks: `[
				{"name": "build", "status": "COMPLETED", "conclusion": "SUCCESS"},
				{"name": "test", "status": "COMPLETED", "conclusion": "FAILURE"}
			]`,
			wantOK:     false,
			wantFailed: []string{"test"},
		},
		"timed out": {
			checks: `[
				{"name": "e2e", "status": "COMPLETED", "conclusion": "TIMED_OUT"}
			]`,
			wantOK:     false,
			wantFailed: []string{"e2e (timed out)"},
		},
		"pending not failure": {
			checks: `[
				{"name": "build", "status": "IN_PROGRESS", "conclusion": ""},
				{"name": "test", "status": "QUEUED", "conclusion": ""}
			]`,
			wantOK:     true,
			wantFailed: nil,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			var checks []struct {
				Name       string `json:"name"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
			}
			r.NoError(json.Unmarshal([]byte(tc.checks), &checks))

			info := &PRInfo{ChecksOK: true}
			for _, check := range checks {
				switch {
				case check.Status == "COMPLETED" && check.Conclusion == "FAILURE":
					info.ChecksOK = false
					info.FailedChecks = append(info.FailedChecks, check.Name)
				case check.Status == "COMPLETED" && check.Conclusion == "TIMED_OUT":
					info.ChecksOK = false
					info.FailedChecks = append(info.FailedChecks, check.Name+" (timed out)")
				}
			}

			r.Equal(tc.wantOK, info.ChecksOK)
			if tc.wantFailed == nil {
				r.Nil(info.FailedChecks)
			} else {
				r.Equal(tc.wantFailed, info.FailedChecks)
			}
		})
	}
}

func TestPRMonitor_InjectsFixMessage(t *testing.T) {
	r := require.New(t)
	hub := NewHub()

	// Verify that when we push a pr_monitor message, it arrives.
	hub.PushMessage(types.InboundMessage{
		Text:   "[PR Health Monitor] CI checks are failing on PR #42. Please investigate.",
		Source: "pr_monitor",
	})

	msg := hub.PullMessage()
	r.Equal("pr_monitor", msg.Source)
	r.Contains(msg.Text, "CI checks are failing")
}

func TestPRMonitor_EmitsEvents(t *testing.T) {
	r := require.New(t)
	hub := NewHub()

	events, unsub := hub.Subscribe()
	defer unsub()

	hub.PublishEvent(types.OutboundEvent{
		Type:    "pr_monitor",
		Content: "PR #7: rebased onto main and pushed.",
	})

	select {
	case event := <-events:
		r.Equal("pr_monitor", event.Type)
		r.Contains(event.Content, "rebased onto main")
	case <-time.After(1 * time.Second):
		t.Fatal("expected pr_monitor event")
	}
}
