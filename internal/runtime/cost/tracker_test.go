package cost

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTrackerBasics(t *testing.T) {
	r := require.New(t)

	// Use temp dir for test database
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	tracker, err := NewTracker()
	r.NoError(err)
	defer func() { _ = tracker.Close() }()

	// Verify database file was created
	dbPath := filepath.Join(tmpDir, ".forge", "costs.db")
	_, err = os.Stat(dbPath)
	r.NoError(err)

	// Track some costs
	err = tracker.Track("session-1", "claude-3-5-sonnet-20241022", 1000, 500, 0, 0, 0.0225)
	r.NoError(err)

	err = tracker.Track("session-1", "claude-3-5-sonnet-20241022", 2000, 800, 100, 50, 0.0565)
	r.NoError(err)

	err = tracker.Track("session-2", "claude-3-haiku-20240307", 500, 200, 0, 0, 0.00038)
	r.NoError(err)

	// Get monthly total
	now := time.Now()
	total, err := tracker.MonthlyTotal(now.Year(), now.Month())
	r.NoError(err)
	r.InDelta(0.0794, total, 0.0001) // Allow for floating point precision
}

func TestDailySummaries(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	tracker, err := NewTracker()
	r.NoError(err)
	defer func() { _ = tracker.Close() }()

	now := time.Now()

	// Track costs on different days
	r.NoError(tracker.Track("session-1", "claude-3-5-sonnet-20241022", 1000, 500, 0, 0, 0.0225))
	time.Sleep(time.Millisecond * 10) // Ensure different timestamps

	// Get daily summaries for current month
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 1, 0)

	summaries, err := tracker.GetDailySummaries(start, end)
	r.NoError(err)
	r.NotEmpty(summaries)

	// Should have today's data
	todaySummary := summaries[0] // Most recent first
	r.Equal(now.Format("2006-01-02"), todaySummary.Date.Format("2006-01-02"))
	r.Equal(1, todaySummary.SessionCount)
	r.Equal(1, todaySummary.CallCount)
	r.Equal(1000, todaySummary.InputTokens)
	r.Equal(500, todaySummary.OutputTokens)
	r.InDelta(0.0225, todaySummary.TotalCost, 0.0001)
}

func TestSessionBreakdown(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	tracker, err := NewTracker()
	r.NoError(err)
	defer func() { _ = tracker.Close() }()

	// Track multiple sessions
	r.NoError(tracker.Track("session-annie", "claude-3-5-sonnet-20241022", 1000, 500, 0, 0, 0.0225))
	time.Sleep(time.Millisecond * 10)
	r.NoError(tracker.Track("session-annie", "claude-3-5-sonnet-20241022", 2000, 800, 0, 0, 0.045))
	time.Sleep(time.Millisecond * 10)
	r.NoError(tracker.Track("session-troy", "claude-3-haiku-20240307", 500, 200, 0, 0, 0.00038))

	now := time.Now()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 1, 0)

	breakdowns, err := tracker.GetSessionBreakdown(start, end)
	r.NoError(err)
	r.Len(breakdowns, 2)

	// Should be sorted by cost descending
	r.Equal("session-annie", breakdowns[0].SessionID)
	r.Equal(2, breakdowns[0].CallCount)
	r.InDelta(0.0675, breakdowns[0].TotalCost, 0.0001)

	r.Equal("session-troy", breakdowns[1].SessionID)
	r.Equal(1, breakdowns[1].CallCount)
	r.InDelta(0.00038, breakdowns[1].TotalCost, 0.00001)
}

// TestEveningLocalCrossesUTCDay pins issue #300: a cost row recorded in the
// evening of a negative-offset zone lands on the *next* UTC calendar day. The
// stats query range is built from local-time boundaries, so the WHERE filter
// must compare against UTC-normalized boundaries or the row vanishes from the
// daily/session views even though MonthlyTotal (already UTC) counts it.
func TestEveningLocalCrossesUTCDay(t *testing.T) {
	r := require.New(t)

	// Force a negative-offset zone so local evening == next-day UTC.
	//
	// SHARP EDGE: t.Setenv("TZ", ...) does NOT update Go's time.Local (that is
	// resolved once at process start). This test deliberately avoids time.Local:
	// every time.Date call below uses the explicit `loc` loaded via
	// time.LoadLocation, so the Go-side boundaries are deterministic regardless
	// of the machine's timezone. The TZ setenv exists solely for the SQLite side:
	// the "localtime" modifier in DATE(timestamp, 'localtime') is evaluated by
	// the sqlite3 C library, which reads the TZ env var at query time. That is
	// what makes the "2026-06-30" daily-bucket assertion below hold in CI (UTC)
	// as well as on a developer's machine. Do NOT rely on time.Now()/time.Local
	// matching `loc` in this test.
	t.Setenv("TZ", "America/Los_Angeles")
	loc, err := time.LoadLocation("America/Los_Angeles")
	r.NoError(err)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	tracker, err := NewTracker()
	r.NoError(err)
	defer func() { _ = tracker.Close() }()

	// SHARP EDGE: the sqlite3 C library resolves the TZ env var via the libc
	// tzset() machinery, which on some platforms caches the zone at first use.
	// If an earlier test in this process already triggered tzset() under a
	// different TZ, our t.Setenv above may not take effect for SQLite's
	// 'localtime' modifier — the assertions below would then flake depending on
	// test order. Rather than assert on a stale zone, probe SQLite directly:
	// convert a known UTC instant to localtime and confirm it reflects the PDT
	// offset. If it doesn't, the C runtime cached a different zone, so skip with
	// a clear diagnostic instead of producing a misleading failure.
	if !sqliteLocaltimeMatches(t, tracker, loc) {
		t.Skip("skipping: sqlite3 C library cached a different TZ (tzset already " +
			"called this process); TZ changes are not reliably picked up mid-run")
	}

	// Local June 30 21:15 PDT == UTC July 1 04:15 (next UTC day). Mirror what
	// Track stores: the UTC instant.
	localEvening := time.Date(2026, 6, 30, 21, 15, 18, 0, loc)
	testInsertRawRecord(t, tracker, "greendale-troy-barnes", localEvening.UTC())

	// Build the query range exactly like cmd/forge/stats.go: local-time
	// month boundaries for the local "today".
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 1, 0)

	summaries, err := tracker.GetDailySummaries(start, end)
	r.NoError(err)
	r.Len(summaries, 1, "evening-local row must appear in daily breakdown")
	r.Equal("2026-06-30", summaries[0].Date.Format("2006-01-02"),
		"daily bucket must use the local day, not the UTC day")
	r.Equal(1, summaries[0].CallCount)

	breakdowns, err := tracker.GetSessionBreakdown(start, end)
	r.NoError(err)
	r.Len(breakdowns, 1, "evening-local row must appear in session breakdown")
	r.Equal("greendale-troy-barnes", breakdowns[0].SessionID)
}

// sqliteLocaltimeMatches reports whether SQLite's 'localtime' modifier resolves
// to the same wall-clock day as loc for a known UTC instant. It guards against
// the libc tzset() caching described in TestEveningLocalCrossesUTCDay: if the C
// library ignored our TZ change, SQLite's localtime will disagree with loc and
// the caller should skip rather than flake.
func sqliteLocaltimeMatches(t *testing.T, tracker *Tracker, loc *time.Location) bool {
	t.Helper()
	// UTC July 1 04:15 == June 30 (evening) in America/Los_Angeles (PDT).
	probe := time.Date(2026, 7, 1, 4, 15, 0, 0, time.UTC)
	wantDay := probe.In(loc).Format("2006-01-02")

	var gotDay string
	err := tracker.db.QueryRow(`SELECT DATE(?, 'localtime')`, probe).Scan(&gotDay)
	require.NoError(t, err)
	return gotDay == wantDay
}

// testInsertRawRecord writes a cost row with a caller-controlled timestamp,// bypassing Track's time.Now(). TEST-ONLY: this helper lives in a _test.go file
// so it is never compiled into the production binary. It exists solely to
// simulate API calls at specific instants (e.g. the UTC/local boundary in
// TestEveningLocalCrossesUTCDay); it must never be promoted to non-test code, as
// arbitrary caller-controlled billing timestamps would corrupt cost accounting.
func testInsertRawRecord(t *testing.T, tracker *Tracker, sessionID string, ts time.Time) {
	t.Helper()
	_, err := tracker.db.Exec(`
		INSERT INTO cost_records
			(timestamp, session_id, model, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, cost)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ts, sessionID, "claude-3-5-sonnet-20241022", 1000, 500, 0, 0, 0.0225)
	require.NoError(t, err)
}

func TestEmptyDatabase(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	tracker, err := NewTracker()
	r.NoError(err)
	defer func() { _ = tracker.Close() }()

	now := time.Now()
	total, err := tracker.MonthlyTotal(now.Year(), now.Month())
	r.NoError(err)
	r.Equal(0.0, total)

	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 1, 0)

	summaries, err := tracker.GetDailySummaries(start, end)
	r.NoError(err)
	r.Empty(summaries)

	breakdowns, err := tracker.GetSessionBreakdown(start, end)
	r.NoError(err)
	r.Empty(breakdowns)
}

func TestPurgeNegativeRowsOnOpen(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	tracker, err := NewTracker()
	r.NoError(err)

	// One good row, several with a negative token column.
	r.NoError(tracker.Track("session-good", "claude-3-5-sonnet-20241022", 1000, 500, 0, 0, 0.0225))
	r.NoError(tracker.Track("session-bad-in", "claude-3-5-sonnet-20241022", -288, 89, 0, 0, 0))
	r.NoError(tracker.Track("session-bad-out", "claude-3-5-sonnet-20241022", 100, -50, 0, 0, 0))
	r.NoError(tracker.Track("session-bad-cc", "claude-3-5-sonnet-20241022", 100, 50, -5, 0, 0))
	r.NoError(tracker.Track("session-bad-cr", "claude-3-5-sonnet-20241022", 100, 50, 0, -5, 0))
	r.NoError(tracker.Close())

	// Reopen: purge should run and drop the four bad rows.
	tracker, err = NewTracker()
	r.NoError(err)
	defer func() { _ = tracker.Close() }()

	now := time.Now()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 1, 0)

	breakdowns, err := tracker.GetSessionBreakdown(start, end)
	r.NoError(err)
	r.Len(breakdowns, 1)
	r.Equal("session-good", breakdowns[0].SessionID)
}
