package agent

import (
	"context"
	"testing"
	"time"

	"github.com/jelmersnoeck/forge/internal/tools"
	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestGHAvailableCache(t *testing.T) {
	r := require.New(t)
	// Just verify the function returns a consistent bool.
	result := tools.GHAvailable()
	r.Equal(result, tools.GHAvailable(), "should return same value on repeated calls")
}

func TestEnsurePR_SkippedWhenGHUnavailable(t *testing.T) {
	r := require.New(t)

	hub := NewHub()
	w := &Worker{
		hub:         hub,
		sessionID:   "greendale-101",
		cwd:         t.TempDir(),
		ghAvailable: false, // simulate gh not installed
	}

	var events []types.OutboundEvent
	emit := func(e types.OutboundEvent) {
		events = append(events, e)
	}

	w.ensurePR(context.Background(), nil, "", emit)

	// No events should be emitted when gh is unavailable.
	r.Empty(events, "should not emit any events when gh unavailable")
}

func TestEnsurePR_NonFatalOnError(t *testing.T) {
	r := require.New(t)

	// Hub is safe to use here — ensurePR only calls phase.EnsurePR
	// (which does git/gh operations) and emit. No session-related
	// Hub methods are invoked.
	hub := NewHub()
	w := &Worker{
		hub:         hub,
		sessionID:   "greendale-102",
		cwd:         t.TempDir(), // not a git repo
		ghAvailable: true,
	}

	var events []types.OutboundEvent
	emit := func(e types.OutboundEvent) {
		events = append(events, e)
	}

	// Should not panic or return error; just log and skip.
	w.ensurePR(context.Background(), nil, "", emit)

	// No pr_url event since it's not a git repo.
	for _, e := range events {
		r.NotEqual("pr_url", e.Type, "should not emit pr_url for non-git repo")
	}
}

func TestEnsurePR_RespectsTimeout(t *testing.T) {
	r := require.New(t)

	hub := NewHub()
	w := &Worker{
		hub:         hub,
		sessionID:   "greendale-103",
		cwd:         t.TempDir(),
		ghAvailable: true,
	}

	var events []types.OutboundEvent
	emit := func(e types.OutboundEvent) {
		events = append(events, e)
	}

	start := time.Now()
	w.ensurePR(context.Background(), nil, "", emit)
	elapsed := time.Since(start)

	// Should complete quickly (not a git repo -> fast bail).
	r.Less(elapsed, 5*time.Second, "should not take long for non-git repo")
}

func TestEnsurePR_SkippedWhenParentContextCancelled(t *testing.T) {
	r := require.New(t)

	hub := NewHub()
	w := &Worker{
		hub:         hub,
		sessionID:   "greendale-104",
		cwd:         t.TempDir(),
		ghAvailable: true,
	}

	var events []types.OutboundEvent
	emit := func(e types.OutboundEvent) {
		events = append(events, e)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling

	start := time.Now()
	w.ensurePR(ctx, nil, "", emit)
	elapsed := time.Since(start)

	// Should bail immediately on cancelled context — no pr_url, no long wait.
	r.Empty(events, "should not emit any events when parent context is cancelled")
	// Verify it bailed fast rather than timing out after 30s.
	r.Less(elapsed, 2*time.Second, "should skip immediately, not wait for timeout")
}
