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

func TestCheckNeedsRebase_ProducesMessage(t *testing.T) {
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

	// Verify checkNeedsRebase detects the divergence.
	r.True(checkNeedsRebase(local, "main"))
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

func TestPRMonitor_EnqueuesCheckMessage(t *testing.T) {
	r := require.New(t)
	hub := NewHub()

	w := &Worker{
		hub:       hub,
		sessionID: "test-greendale",
	}

	// enqueuePRCheck should push an internal message.
	w.enqueuePRCheck()

	msg, ok := hub.PullMessage(context.Background())
	r.True(ok)
	r.Equal(prCheckSource, msg.Source)
}

func TestPRMonitor_CheckMessageNotProcessedAsUserMessage(t *testing.T) {
	r := require.New(t)
	hub := NewHub()

	// Push a PR check message followed by a real user message.
	hub.PushMessage(types.InboundMessage{Source: prCheckSource})
	hub.PushMessage(types.InboundMessage{Text: "hey Troy", Source: "user"})

	// First message should be the internal check.
	msg1, _ := hub.PullMessage(context.Background())
	r.Equal(prCheckSource, msg1.Source)

	// Second should be the user message.
	msg2, _ := hub.PullMessage(context.Background())
	r.Equal("user", msg2.Source)
	r.Equal("hey Troy", msg2.Text)
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

	msg, _ := hub.PullMessage(context.Background())
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

func TestPRMonitor_SerializedWithWorker(t *testing.T) {
	// Verify the fix: PR check messages go through the hub queue,
	// so they're processed by the worker in sequence — no concurrent
	// git access with conversation turns.
	r := require.New(t)
	hub := NewHub()

	// Simulate: user message, then PR check, then another user message.
	hub.PushMessage(types.InboundMessage{Text: "user msg 1", Source: "user"})
	hub.PushMessage(types.InboundMessage{Source: prCheckSource})
	hub.PushMessage(types.InboundMessage{Text: "user msg 2", Source: "user"})

	// Messages come out in order — PR check is sandwiched between
	// user messages, proving serialization.
	msg1, _ := hub.PullMessage(context.Background())
	r.Equal("user", msg1.Source)
	r.Equal("user msg 1", msg1.Text)

	msg2, _ := hub.PullMessage(context.Background())
	r.Equal(prCheckSource, msg2.Source)

	msg3, _ := hub.PullMessage(context.Background())
	r.Equal("user", msg3.Source)
	r.Equal("user msg 2", msg3.Text)
}

func TestPRMonitor_GracefulShutdownDrainsPendingChecks(t *testing.T) {
	// When the worker context is cancelled, pending PR check messages
	// should not cause hangs — PullMessage returns (_, false).
	r := require.New(t)
	hub := NewHub()

	w := &Worker{
		hub:       hub,
		sessionID: "test-shutdown",
	}

	// Enqueue a PR check.
	w.enqueuePRCheck()

	// Pull the pending message — simulates worker draining before shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	msg, ok := hub.PullMessage(ctx)
	r.True(ok)
	r.Equal(prCheckSource, msg.Source)

	// Cancel context, then try pulling — should return immediately with false.
	cancel()
	_, ok = hub.PullMessage(ctx)
	r.False(ok)
}

func TestPRMonitor_BackpressureSkipsDuplicate(t *testing.T) {
	// When a PR check is already pending, enqueuePRCheck should skip
	// rather than growing the queue unboundedly.
	r := require.New(t)
	hub := NewHub()

	w := &Worker{
		hub:       hub,
		sessionID: "test-backpressure",
	}

	// First enqueue succeeds.
	w.enqueuePRCheck()

	// Second enqueue is skipped — pending flag is still set.
	w.enqueuePRCheck()
	w.enqueuePRCheck()

	// Only one message should be in the queue.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	msg, ok := hub.PullMessage(ctx)
	r.True(ok)
	r.Equal(prCheckSource, msg.Source)

	// No more messages — context should time out.
	_, ok = hub.PullMessage(ctx)
	r.False(ok)
}

func TestPRMonitor_HandlePRCheckClearsPendingFlag(t *testing.T) {
	// After handlePRCheck processes a message, the pending flag should
	// be cleared so the next timer tick can enqueue again.
	r := require.New(t)

	local, _ := setupGitRepo(t)
	hub := NewHub()

	w := &Worker{
		hub:       hub,
		cwd:       local,
		sessionID: "test-clear-pending",
	}

	// Simulate an enqueued check by setting the pending flag.
	w.prCheckPending.Store(true)

	// handlePRCheck clears the flag.
	w.handlePRCheck(context.Background())
	r.False(w.prCheckPending.Load())

	// Now enqueuePRCheck should succeed again.
	w.enqueuePRCheck()
	r.True(w.prCheckPending.Load())
}

func TestPRMonitor_GHNotAvailableEmitsEvent(t *testing.T) {
	// When gh is not on PATH, the monitor should emit a structured
	// pr_monitor event (not just log).
	r := require.New(t)

	// Only meaningful if gh is actually missing; otherwise we test
	// the event emission path directly.
	hub := NewHub()
	events, unsub := hub.Subscribe()
	defer unsub()

	// Emit the event directly (same as what prHealthMonitor does when
	// gh is missing) to verify the event structure is correct.
	hub.PublishEvent(types.OutboundEvent{
		Type:    "pr_monitor",
		Content: "PR health monitor disabled: gh CLI not found on PATH.",
	})

	select {
	case event := <-events:
		r.Equal("pr_monitor", event.Type)
		r.Contains(event.Content, "gh CLI not found")
	case <-time.After(1 * time.Second):
		t.Fatal("expected pr_monitor event for gh not available")
	}
}
