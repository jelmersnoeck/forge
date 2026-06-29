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
// to the cost DB. The DB write is skipped when the tracker is nil, the event
// lacks usage/model, or the delta is non-positive (e.g. usage reset on resume).
// A non-empty warning is returned only when the DB write itself fails; cost
// tracking failures are never fatal.
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

	delta := types.TokenUsage{
		InputTokens:         ev.Usage.InputTokens - c.lastTracked.InputTokens,
		OutputTokens:        ev.Usage.OutputTokens - c.lastTracked.OutputTokens,
		CacheCreationTokens: ev.Usage.CacheCreationTokens - c.lastTracked.CacheCreationTokens,
		CacheReadTokens:     ev.Usage.CacheReadTokens - c.lastTracked.CacheReadTokens,
	}

	if delta.InputTokens <= 0 && delta.OutputTokens <= 0 &&
		delta.CacheCreationTokens <= 0 && delta.CacheReadTokens <= 0 {
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

	c.lastTracked = *ev.Usage
	return ""
}
