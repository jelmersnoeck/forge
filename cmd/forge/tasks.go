package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// TaskTrackerSet owns the live inline task-progress trackers, keyed by task ID,
// with a stable insertion order for rendering.
type TaskTrackerSet struct {
	trackers map[string]*taskTracker
	order    []string
}

// NewTaskTrackerSet returns an empty tracker set.
func NewTaskTrackerSet() *TaskTrackerSet {
	return &TaskTrackerSet{
		trackers: make(map[string]*taskTracker),
	}
}

// Len returns the number of live trackers.
func (s *TaskTrackerSet) Len() int { return len(s.trackers) }

// HandleStatus parses a task_status payload and updates (or creates) the tracker
// for that task. When the task reaches a terminal state the tracker is finalized
// and removed; the returned summary line should be appended to scrollback by the
// caller. terminal reports whether a terminal status was processed.
//
// Malformed JSON returns ("", false) and creates no tracker.
func (s *TaskTrackerSet) HandleStatus(content string) (summary string, terminal bool) {
	var payload struct {
		ID          string   `json:"id"`
		Description string   `json:"description"`
		Status      string   `json:"status"`
		OutputTail  []string `json:"outputTail"`
		Duration    string   `json:"duration"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return "", false
	}

	tt, exists := s.trackers[payload.ID]
	if !exists {
		tt = &taskTracker{
			taskID:    payload.ID,
			startTime: time.Now(),
		}
		s.trackers[payload.ID] = tt
		s.order = append(s.order, payload.ID)
	}

	tt.description = payload.Description
	tt.status = payload.Status
	tt.outputTail = payload.OutputTail
	tt.duration = payload.Duration

	switch tt.status {
	case "completed", "failed", "killed":
		return s.finalize(tt), true
	}
	return "", false
}

// FinalizeAll finalizes every remaining tracker (e.g. when the agent is done)
// and returns the summary lines in stable order.
func (s *TaskTrackerSet) FinalizeAll() []string {
	var summaries []string
	for _, id := range append([]string{}, s.order...) {
		if tt, ok := s.trackers[id]; ok {
			summaries = append(summaries, s.finalize(tt))
		}
	}
	return summaries
}

// finalize removes a finished task from the live set and returns its one-line
// scrollback summary.
func (s *TaskTrackerSet) finalize(tt *taskTracker) string {
	icon := dimStyle.Render("?")
	switch tt.status {
	case "completed":
		icon = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("✓")
	case "failed":
		icon = errorStyle.Render("✗")
	case "killed":
		icon = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render("⊘")
	}

	dur := tt.duration
	if dur == "" {
		dur = time.Since(tt.startTime).Round(time.Second).String()
	}

	summary := fmt.Sprintf("  %s %s (%s) %s",
		icon,
		tt.description,
		tt.taskID,
		dimStyle.Render(dur),
	)

	delete(s.trackers, tt.taskID)
	for i, id := range s.order {
		if id == tt.taskID {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	return summary
}

// Height returns how many terminal lines the tracker block occupies: one header
// line per task plus its output tail.
func (s *TaskTrackerSet) Height() int {
	h := 0
	for _, id := range s.order {
		if tt, ok := s.trackers[id]; ok {
			h++
			h += len(tt.outputTail)
		}
	}
	return h
}

// Render produces the live task-progress block shown between the main output
// area and the thinking indicator.
//
//	⠹ Running tests (b3)                 running
//	    PASS TestFoo
//	    PASS TestBar
func (s *TaskTrackerSet) Render(spinner string, width int) string {
	if len(s.trackers) == 0 {
		return ""
	}

	var lines []string
	for _, id := range s.order {
		tt, ok := s.trackers[id]
		if !ok {
			continue
		}

		statusColor := lipgloss.Color("6") // cyan = running
		switch tt.status {
		case "pending":
			statusColor = lipgloss.Color("8")
		}
		statusStr := lipgloss.NewStyle().Foreground(statusColor).Render(tt.status)

		header := fmt.Sprintf("  %s %s %s %s",
			thinkingStyle.Render(spinner),
			tt.description,
			dimStyle.Render("("+tt.taskID+")"),
			statusStr,
		)
		lines = append(lines, header)

		maxWidth := width - 8
		if maxWidth < 40 {
			maxWidth = 40
		}
		for _, ol := range tt.outputTail {
			if len(ol) > maxWidth {
				ol = ol[:maxWidth]
			}
			lines = append(lines, "      "+dimStyle.Render(ol))
		}
	}

	return strings.Join(lines, "\n")
}
