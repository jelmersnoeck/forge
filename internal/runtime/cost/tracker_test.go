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
	home := os.Getenv("HOME")
	defer os.Setenv("HOME", home)
	os.Setenv("HOME", tmpDir)

	tracker, err := NewTracker()
	r.NoError(err)
	defer tracker.Close()

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
	home := os.Getenv("HOME")
	defer os.Setenv("HOME", home)
	os.Setenv("HOME", tmpDir)

	tracker, err := NewTracker()
	r.NoError(err)
	defer tracker.Close()

	now := time.Now()

	// Track costs on different days
	tracker.Track("session-1", "claude-3-5-sonnet-20241022", 1000, 500, 0, 0, 0.0225)
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
	home := os.Getenv("HOME")
	defer os.Setenv("HOME", home)
	os.Setenv("HOME", tmpDir)

	tracker, err := NewTracker()
	r.NoError(err)
	defer tracker.Close()

	// Track multiple sessions
	tracker.Track("session-annie", "claude-3-5-sonnet-20241022", 1000, 500, 0, 0, 0.0225)
	time.Sleep(time.Millisecond * 10)
	tracker.Track("session-annie", "claude-3-5-sonnet-20241022", 2000, 800, 0, 0, 0.045)
	time.Sleep(time.Millisecond * 10)
	tracker.Track("session-troy", "claude-3-haiku-20240307", 500, 200, 0, 0, 0.00038)

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

func TestEmptyDatabase(t *testing.T) {
	r := require.New(t)

	tmpDir := t.TempDir()
	home := os.Getenv("HOME")
	defer os.Setenv("HOME", home)
	os.Setenv("HOME", tmpDir)

	tracker, err := NewTracker()
	r.NoError(err)
	defer tracker.Close()

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
