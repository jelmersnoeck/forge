package main

import (
	"github.com/jelmersnoeck/forge/internal/runtime/cost"
	"github.com/jelmersnoeck/forge/internal/types"
)

// CostAccumulator tracks cumulative session token usage and the active model
// name, and persists per-call deltas to the cost tracker.
type CostAccumulator struct {
	total       types.TokenUsage
	lastTracked types.TokenUsage
	modelName   string
}

// SetModel records the active model name (used for cost calculation/display).
func (c *CostAccumulator) SetModel(name string) { c.modelName = name }

// Summary returns the cumulative usage and active model name for the status line.
func (c *CostAccumulator) Summary() (types.TokenUsage, string) {
	return c.total, c.modelName
}

// Record applies a usage event: it updates the cumulative total and model name
// from whatever the event carries, then persists the delta since the last track
// to the cost DB. Each delta field is clamped at zero, so a usage decrease (e.g.
// session resume or subagent usage reset) never writes negative token counts.
// The baseline is re-based per-field to max(lastTracked, usage) so a drop in one
// field cannot cause perpetually negative deltas. The DB write is skipped when
// the tracker is nil, the event lacks usage/model, or the clamped delta is all
// zero. A non-empty warning is returned only when the DB write itself fails;
// cost tracking failures are never fatal.
func (c *CostAccumulator) Record(ev types.OutboundEvent, t *cost.Tracker, sessionID string) (warning string) {
	if ev.Usage != nil {
		c.total = *ev.Usage
	}
	if ev.Model != "" {
		c.modelName = ev.Model
	}

	if t == nil || ev.Usage == nil || ev.Model == "" {
		return ""
	}

	delta := clampNonNegative(types.TokenUsage{
		InputTokens:         ev.Usage.InputTokens - c.lastTracked.InputTokens,
		OutputTokens:        ev.Usage.OutputTokens - c.lastTracked.OutputTokens,
		CacheCreationTokens: ev.Usage.CacheCreationTokens - c.lastTracked.CacheCreationTokens,
		CacheReadTokens:     ev.Usage.CacheReadTokens - c.lastTracked.CacheReadTokens,
	})

	// Advance the baseline to the latest usage even when we skip the write. On a
	// usage drop (resume/subagent reset) this re-bases the field down so the next
	// growth is measured against the fresh lower cumulative count rather than a
	// stale high-water mark that would swallow it.
	c.lastTracked = *ev.Usage

	if delta.InputTokens == 0 && delta.OutputTokens == 0 &&
		delta.CacheCreationTokens == 0 && delta.CacheReadTokens == 0 {
		return ""
	}

	callCost := cost.Calculate(ev.Model, delta)
	if err := t.Track(
		sessionID,
		ev.Model,
		delta.InputTokens,
		delta.OutputTokens,
		delta.CacheCreationTokens,
		delta.CacheReadTokens,
		callCost,
	); err != nil {
		return "  ⚠  cost tracking error: " + err.Error()
	}

	return ""
}

// clampNonNegative returns u with every token field floored at 0.
func clampNonNegative(u types.TokenUsage) types.TokenUsage {
	if u.InputTokens < 0 {
		u.InputTokens = 0
	}
	if u.OutputTokens < 0 {
		u.OutputTokens = 0
	}
	if u.CacheCreationTokens < 0 {
		u.CacheCreationTokens = 0
	}
	if u.CacheReadTokens < 0 {
		u.CacheReadTokens = 0
	}
	return u
}
