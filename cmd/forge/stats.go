package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/jelmersnoeck/forge/internal/runtime/cost"
)

var (
	tableHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	totalStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))
)

func runStats(args []string) int {
	fs := flag.NewFlagSet("forge stats", flag.ExitOnError)
	month := fs.String("month", "", "show stats for month (YYYY-MM, e.g. 2026-04)")
	week := fs.Bool("week", false, "show current week stats")
	daily := fs.Bool("daily", false, "show daily breakdown")
	sessions := fs.Bool("sessions", false, "show per-session breakdown")
	fs.Parse(args[1:])

	tracker, err := cost.NewTracker()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: could not open cost database: %v\n", err)
		return 1
	}
	defer tracker.Close()

	var start, end time.Time

	switch {
	case *month != "":
		// Parse YYYY-MM
		t, err := time.Parse("2006-01", *month)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: invalid month format (use YYYY-MM): %v\n", err)
			return 1
		}
		start = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.Local)
		end = start.AddDate(0, 1, 0)
		fmt.Printf("%s %s\n\n", tableHeaderStyle.Render("Monthly Stats:"), t.Format("January 2006"))

	case *week:
		now := time.Now()
		weekday := int(now.Weekday())
		start = now.AddDate(0, 0, -weekday).Truncate(24 * time.Hour)
		end = start.AddDate(0, 0, 7)
		fmt.Printf("%s %s - %s\n\n",
			tableHeaderStyle.Render("Weekly Stats:"),
			start.Format("Jan 2"),
			end.AddDate(0, 0, -1).Format("Jan 2, 2006"))

	default:
		// Current month by default
		now := time.Now()
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
		end = start.AddDate(0, 1, 0)
		fmt.Printf("%s %s\n\n", tableHeaderStyle.Render("Monthly Stats:"), now.Format("January 2006"))
	}

	// Get monthly total
	total, err := tracker.MonthlyTotal(start.Year(), start.Month())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: could not fetch monthly total: %v\n", err)
		return 1
	}

	fmt.Printf("%s %s\n\n", totalStyle.Render("Total:"), cost.FormatCost(total))

	// Daily breakdown
	if *daily || !*sessions {
		summaries, err := tracker.GetDailySummaries(start, end)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: could not fetch daily summaries: %v\n", err)
			return 1
		}

		if len(summaries) == 0 {
			fmt.Println(dimStyle.Render("No data for this period"))
			return 0
		}

		fmt.Println(tableHeaderStyle.Render("Daily Breakdown:"))
		fmt.Println()
		fmt.Printf("%-12s %12s %8s %7s %15s %15s %20s %20s\n",
			"Date", "Cost", "Sessions", "Calls", "Input Tokens", "Output Tokens", "Cache Write", "Cache Read")
		fmt.Println(dimStyle.Render("─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────"))

		for _, s := range summaries {
			totalTokens := s.InputTokens + s.OutputTokens + s.CacheCreationTokens + s.CacheReadTokens
			fmt.Printf("%-12s %12s %8d %7d %15s %15s %20s %20s\n",
				s.Date.Format("Jan 2"),
				cost.FormatCost(s.TotalCost),
				s.SessionCount,
				s.CallCount,
				cost.FormatNumber(s.InputTokens),
				cost.FormatNumber(s.OutputTokens),
				cost.FormatNumberWithPercent(s.CacheCreationTokens, totalTokens),
				cost.FormatNumberWithPercent(s.CacheReadTokens, totalTokens),
			)
		}
		fmt.Println()
	}

	// Session breakdown
	if *sessions {
		breakdowns, err := tracker.GetSessionBreakdown(start, end)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: could not fetch session breakdown: %v\n", err)
			return 1
		}

		if len(breakdowns) == 0 {
			fmt.Println(dimStyle.Render("No sessions in this period"))
			return 0
		}

		fmt.Println(tableHeaderStyle.Render("Session Breakdown:"))
		fmt.Println()
		fmt.Printf("%-38s %12s %7s %15s %15s %20s %20s %12s\n",
			"Session ID", "Cost", "Calls", "Input Tokens", "Output Tokens", "Cache Write", "Cache Read", "Duration")
		fmt.Println(dimStyle.Render("───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────"))

		for _, b := range breakdowns {
			duration := b.LastCall.Sub(b.FirstCall)
			durationStr := formatDuration(duration)

			totalTokens := b.InputTokens + b.OutputTokens + b.CacheCreationTokens + b.CacheReadTokens
			fmt.Printf("%-38s %12s %7d %15s %15s %20s %20s %12s\n",
				truncate(b.SessionID, 36),
				cost.FormatCost(b.TotalCost),
				b.CallCount,
				cost.FormatNumber(b.InputTokens),
				cost.FormatNumber(b.OutputTokens),
				cost.FormatNumberWithPercent(b.CacheCreationTokens, totalTokens),
				cost.FormatNumberWithPercent(b.CacheReadTokens, totalTokens),
				durationStr,
			)
		}
		fmt.Println()
	}

	return 0
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
