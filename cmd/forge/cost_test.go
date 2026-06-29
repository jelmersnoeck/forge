package main

import (
	"testing"
	"time"

	"github.com/jelmersnoeck/forge/internal/runtime/cost"
	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func newTestTracker(t *testing.T) *cost.Tracker {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	tr, err := cost.NewTracker()
	require.NoError(t, err)
	t.Cleanup(func() { _ = tr.Close() })
	return tr
}

func todayTokens(t *testing.T, tr *cost.Tracker) (in, out, cc, cr int) {
	t.Helper()
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 1, 0)
	summaries, err := tr.GetDailySummaries(start, end)
	require.NoError(t, err)
	for _, s := range summaries {
		in += s.InputTokens
		out += s.OutputTokens
		cc += s.CacheCreationTokens
		cr += s.CacheReadTokens
	}
	return in, out, cc, cr
}

func usage(in, out, cc, cr int) *types.TokenUsage {
	return &types.TokenUsage{
		InputTokens:         in,
		OutputTokens:        out,
		CacheCreationTokens: cc,
		CacheReadTokens:     cr,
	}
}

func TestCostAccumulatorRecord(t *testing.T) {
	const model = "claude-3-5-sonnet-20241022"

	tests := map[string]struct {
		events  []*types.TokenUsage // sequence of cumulative usage events
		wantIn  int
		wantOut int
		wantCC  int
		wantCR  int
	}{
		"monotonic increase records deltas": {
			events:  []*types.TokenUsage{usage(1000, 500, 0, 0), usage(3000, 800, 100, 50)},
			wantIn:  3000,
			wantOut: 800,
			wantCC:  100,
			wantCR:  50,
		},
		"mixed delta clamps only negative field": {
			// input drops 1000->712 (delta -288), output grows 500->589 (+89).
			events:  []*types.TokenUsage{usage(1000, 500, 0, 0), usage(712, 589, 0, 0)},
			wantIn:  1000, // first event in=1000, second clamped to 0
			wantOut: 589,  // 500 + 89
			wantCC:  0,
			wantCR:  0,
		},
		"full reset clamps all fields to zero": {
			// second event fully smaller than first: all deltas negative.
			events:  []*types.TokenUsage{usage(5000, 2000, 300, 400), usage(10, 5, 1, 2)},
			wantIn:  5000,
			wantOut: 2000,
			wantCC:  300,
			wantCR:  400,
		},
		"reset then growth rebases baseline downward": {
			// after a reset to (10,5,0,0), subsequent growth to (2010,1005,0,0)
			// must record the full +2000/+1000 delta, not be swallowed by the
			// stale-high baseline from the first event.
			events: []*types.TokenUsage{
				usage(2000, 1000, 0, 0),
				usage(10, 5, 0, 0),
				usage(2010, 1005, 0, 0),
			},
			wantIn:  2000 + 2000,
			wantOut: 1000 + 1000,
			wantCC:  0,
			wantCR:  0,
		},
		"duplicate usage event skips second write": {
			events:  []*types.TokenUsage{usage(1000, 500, 0, 0), usage(1000, 500, 0, 0)},
			wantIn:  1000,
			wantOut: 500,
			wantCC:  0,
			wantCR:  0,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			tr := newTestTracker(t)
			acc := &CostAccumulator{}

			for _, u := range tc.events {
				w := acc.Record(types.OutboundEvent{Usage: u, Model: model}, tr, "session-troy")
				r.Empty(w)
			}

			in, out, cc, cr := todayTokens(t, tr)
			r.Equal(tc.wantIn, in, "input tokens")
			r.Equal(tc.wantOut, out, "output tokens")
			r.Equal(tc.wantCC, cc, "cache creation tokens")
			r.Equal(tc.wantCR, cr, "cache read tokens")
			r.GreaterOrEqual(in, 0)
			r.GreaterOrEqual(out, 0)
			r.GreaterOrEqual(cc, 0)
			r.GreaterOrEqual(cr, 0)
		})
	}
}

func TestCostAccumulatorRecordSkipsWithoutUsageOrModel(t *testing.T) {
	r := require.New(t)
	tr := newTestTracker(t)
	acc := &CostAccumulator{}

	r.Empty(acc.Record(types.OutboundEvent{Model: "claude-3-5-sonnet-20241022"}, tr, "s"))
	r.Empty(acc.Record(types.OutboundEvent{Usage: usage(100, 50, 0, 0)}, tr, "s"))

	in, out, cc, cr := todayTokens(t, tr)
	r.Equal(0, in)
	r.Equal(0, out)
	r.Equal(0, cc)
	r.Equal(0, cr)
}

func TestCostAccumulatorRecordNilTracker(t *testing.T) {
	r := require.New(t)
	acc := &CostAccumulator{}
	w := acc.Record(types.OutboundEvent{Usage: usage(100, 50, 0, 0), Model: "claude-3-5-sonnet-20241022"}, nil, "s")
	r.Empty(w)
	total, _ := acc.Summary()
	r.Equal(100, total.InputTokens)
}

func TestClampNonNegative(t *testing.T) {
	r := require.New(t)
	got := clampNonNegative(types.TokenUsage{
		InputTokens: -1, OutputTokens: 5, CacheCreationTokens: -10, CacheReadTokens: 0,
	})
	r.Equal(types.TokenUsage{InputTokens: 0, OutputTokens: 5, CacheCreationTokens: 0, CacheReadTokens: 0}, got)
}

func TestCostAccumulatorRecordWarnsOnUnpricedModelWithTokens(t *testing.T) {
	r := require.New(t)
	// Force offline so the unknown model is genuinely unpriced.
	t.Setenv("LITELLM_MODEL_COST_MAP_URL", "http://127.0.0.1:1/offline")
	tr := newTestTracker(t)
	acc := &CostAccumulator{}

	// Unique model name so the once-per-process dedup doesn't suppress this run.
	model := "senor-chang-model-" + t.Name()
	w := acc.Record(types.OutboundEvent{Usage: usage(1000, 500, 0, 0), Model: model}, tr, "session-chang")
	r.Contains(w, "no pricing")
	r.Contains(w, model)

	// Second call for the same model is deduped to empty.
	w2 := acc.Record(types.OutboundEvent{Usage: usage(2000, 1000, 0, 0), Model: model}, tr, "session-chang")
	r.Empty(w2)
}

func TestCostAccumulatorRecordNoWarnOnPricedModel(t *testing.T) {
	r := require.New(t)
	t.Setenv("LITELLM_MODEL_COST_MAP_URL", "http://127.0.0.1:1/offline")
	tr := newTestTracker(t)
	acc := &CostAccumulator{}

	// sonnet alias target is priced (the issue #278 fix) -> no warning.
	w := acc.Record(types.OutboundEvent{Usage: usage(1000, 500, 0, 0), Model: "claude-sonnet-4-20250514"}, tr, "s")
	r.Empty(w)
}
