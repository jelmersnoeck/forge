package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

// TestBashIdleWatchdog verifies that the idle watchdog fires when a command
// produces output then goes silent.
func TestBashIdleWatchdog(t *testing.T) {
	r := require.New(t)

	// Command: print one line, then sleep forever.
	// We override the idle timeout via a short duration in the test.
	// Since we can't change the const, we use a command that will trigger
	// the idle watchdog within the default 30s... but that's too slow for
	// a test. Instead, test that context cancellation works properly and
	// that output is captured.
	//
	// For a proper idle test we'd need to make the timeout configurable.
	// For now, verify the plumbing: output streaming + context cancel.

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var progressMsgs []string
	var mu sync.Mutex

	result, err := bashHandler(map[string]any{
		"command": "echo 'Greendale Community College'; sleep 10",
		"timeout": float64(3000), // 3s timeout
	}, types.ToolContext{
		Ctx: ctx,
		CWD: t.TempDir(),
		Emit: func(event types.OutboundEvent) {
			mu.Lock()
			defer mu.Unlock()
			if event.Type == "tool_progress" {
				progressMsgs = append(progressMsgs, event.Content)
			}
		},
	})

	r.NoError(err)
	r.True(result.IsError, "should be an error due to timeout")
	r.Contains(result.Content[0].Text, "Greendale Community College",
		"output captured before timeout should be present")
	r.Contains(result.Content[0].Text, "timed out",
		"should mention timeout")
}

// TestBashProcessGroupKill verifies that child processes are killed when
// the context is cancelled (the process group fix).
func TestBashProcessGroupKill(t *testing.T) {
	r := require.New(t)

	// Start a command that spawns a child process, then cancel.
	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	var result types.ToolResult
	var err error

	wg.Add(1)
	go func() {
		defer wg.Done()
		result, err = bashHandler(map[string]any{
			"command": "bash -c 'echo Human Being mascot; sleep 60'",
			"timeout": float64(30000),
		}, types.ToolContext{
			Ctx: ctx,
			CWD: t.TempDir(),
		})
	}()

	// Give it a moment to start, then cancel.
	time.Sleep(1 * time.Second)
	cancel()
	wg.Wait()

	r.NoError(err)
	r.True(result.IsError)
	r.Contains(result.Content[0].Text, "Human Being mascot",
		"should have captured output before cancellation")
	r.Contains(result.Content[0].Text, "interrupted",
		"should mention interruption")
}

// TestBashProgressEvents verifies that tool_progress events are emitted
// for longer-running commands.
func TestBashProgressEvents(t *testing.T) {
	r := require.New(t)

	var progressMsgs []string
	var mu sync.Mutex

	// Run a command that takes ~12s — should get at least one progress event
	// at the 10s mark.
	result, err := bashHandler(map[string]any{
		"command": "echo 'Senor Chang says hi'; sleep 12; echo 'done'",
		"timeout": float64(15000),
	}, types.ToolContext{
		Ctx: context.Background(),
		CWD: t.TempDir(),
		Emit: func(event types.OutboundEvent) {
			mu.Lock()
			defer mu.Unlock()
			if event.Type == "tool_progress" {
				progressMsgs = append(progressMsgs, event.Content)
			}
		},
	})

	r.NoError(err)
	r.False(result.IsError)
	r.Contains(result.Content[0].Text, "Senor Chang says hi")
	r.Contains(result.Content[0].Text, "done")

	mu.Lock()
	defer mu.Unlock()
	r.NotEmpty(progressMsgs, "should have received at least one progress event")
	r.Contains(progressMsgs[0], "elapsed", "progress should contain elapsed time")
}

// TestBashNilEmit verifies the handler doesn't panic when Emit is nil.
func TestBashNilEmit(t *testing.T) {
	r := require.New(t)

	result, err := bashHandler(map[string]any{
		"command": "echo 'Annie Edison'",
	}, types.ToolContext{
		Ctx: context.Background(),
		CWD: t.TempDir(),
	})

	r.NoError(err)
	r.False(result.IsError)
	r.Contains(result.Content[0].Text, "Annie Edison")
}

// TestBashFastCommandUnchanged verifies fast commands behave identically.
func TestBashFastCommandUnchanged(t *testing.T) {
	r := require.New(t)

	result, err := bashHandler(map[string]any{
		"command": "echo 'Jeff Winger'; echo 'Britta Perry' >&2",
	}, types.ToolContext{
		Ctx: context.Background(),
		CWD: t.TempDir(),
	})

	r.NoError(err)
	r.False(result.IsError)
	output := result.Content[0].Text
	r.Contains(output, "Jeff Winger")
	r.Contains(output, "Britta Perry")
}

// TestBashDiagnostics verifies gatherDiagnostics doesn't panic/hang.
func TestBashDiagnostics(t *testing.T) {
	// Should return quickly even with a bogus PID.
	result := gatherDiagnostics(999999999)
	// We don't assert specific content since ps/lsof may fail for a bogus PID,
	// but it should not hang and should return something.
	t.Logf("diagnostics for bogus PID: %s", result)

	// PID 0 should return the no-PID message.
	result = gatherDiagnostics(0)
	require.Contains(t, result, "no PID")
}

// TestBashTruncateCommand verifies the truncation helper.
func TestBashTruncateCommand(t *testing.T) {
	tests := map[string]struct {
		input  string
		maxLen int
		want   string
	}{
		"short":  {input: "echo hi", maxLen: 20, want: "echo hi"},
		"exact":  {input: "echo hello", maxLen: 10, want: "echo hello"},
		"long":   {input: "docker run --rm internal-fly-gateway-test caddy version", maxLen: 20, want: "docker run --rm i..."},
		"padded": {input: "  echo hi  ", maxLen: 20, want: "echo hi"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := truncateCommand(tc.input, tc.maxLen)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestBashIdleWatchdogFires verifies the idle watchdog mechanism by using
// a short idle timeout. We achieve this by having the command output once
// then go silent, with the hard timeout set high enough that the idle
// detection runs first.
//
// NOTE: This test takes ~35s due to the 30s default idle timeout.
// It's here for correctness but marked with a build tag comment.
func TestBashIdleWatchdogFires(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping idle watchdog test in short mode (takes ~35s)")
	}

	r := require.New(t)

	start := time.Now()
	result, err := bashHandler(map[string]any{
		// Print output, then sleep forever. Idle watchdog should fire at ~30s.
		"command": "echo 'Dean Pelton'; sleep 120",
		"timeout": float64(120000), // 2 min hard timeout — watchdog should fire first
	}, types.ToolContext{
		Ctx: context.Background(),
		CWD: t.TempDir(),
	})
	elapsed := time.Since(start)

	r.NoError(err)
	r.True(result.IsError, "idle watchdog should return error result")

	output := result.Content[0].Text
	r.Contains(output, "Dean Pelton", "captured output should be present")
	r.Contains(output, "no new output", "should mention idle detection")
	r.Contains(output, "still running", "should say process is running")
	r.Contains(output, "Process diagnostics", "should include diagnostics")

	// Should have returned in ~30-35s, not 120s.
	r.Less(elapsed, 50*time.Second,
		"should return after idle timeout (~30s), not hard timeout (120s)")

	// Verify it mentions the PID for the LLM to use.
	r.True(strings.Contains(output, "PID:") || strings.Contains(output, "kill"),
		"should provide PID or kill instructions")
}
