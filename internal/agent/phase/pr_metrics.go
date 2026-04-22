package phase

import "sync/atomic"

// PRMetrics encapsulates atomic counters for PR operation observability.
// Access counters via the exported methods — never manipulate fields directly.
type PRMetrics struct {
	pushFailures      atomic.Int64
	invalidURLResults atomic.Int64
}

// prMetrics is the singleton metrics instance for the package.
var prMetrics = &PRMetrics{}

// PRMetricsInstance returns the package-level metrics for external monitoring.
func PRMetricsInstance() *PRMetrics { return prMetrics }

// RecordPushFailure increments the push failure counter.
func (m *PRMetrics) RecordPushFailure() { m.pushFailures.Add(1) }

// RecordInvalidURLResult increments the invalid URL response counter.
func (m *PRMetrics) RecordInvalidURLResult() { m.invalidURLResults.Add(1) }

// PushFailures returns the current push failure count.
func (m *PRMetrics) PushFailures() int64 { return m.pushFailures.Load() }

// InvalidURLResults returns the current invalid URL response count.
func (m *PRMetrics) InvalidURLResults() int64 { return m.invalidURLResults.Load() }

// PRMetricsSnapshot is a point-in-time copy of all PR metrics.
type PRMetricsSnapshot struct {
	PushFailures      int64
	InvalidURLResults int64
}

// Snapshot returns a point-in-time copy of all counters.
func (m *PRMetrics) Snapshot() PRMetricsSnapshot {
	return PRMetricsSnapshot{
		PushFailures:      m.pushFailures.Load(),
		InvalidURLResults: m.invalidURLResults.Load(),
	}
}

// Reset zeroes all counters. Intended for testing or metric rotation.
func (m *PRMetrics) Reset() {
	m.pushFailures.Store(0)
	m.invalidURLResults.Store(0)
}
