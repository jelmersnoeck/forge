package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHandleTaskStatus_CreatesTracker(t *testing.T) {
	r := require.New(t)

	s := NewTaskTrackerSet()

	payload := map[string]any{
		"id":          "b3",
		"description": "Running tests at Greendale",
		"status":      "running",
		"outputTail":  []string{"PASS TestPaintball", "PASS TestDarkTimeline"},
	}
	data, _ := json.Marshal(payload)
	summary, terminal := s.HandleStatus(string(data))
	r.Empty(summary)
	r.False(terminal)

	r.Equal(1, s.Len())
	tt := s.trackers["b3"]
	r.NotNil(tt)
	r.Equal("b3", tt.taskID)
	r.Equal("Running tests at Greendale", tt.description)
	r.Equal("running", tt.status)
	r.Equal([]string{"PASS TestPaintball", "PASS TestDarkTimeline"}, tt.outputTail)
	r.Equal([]string{"b3"}, s.order)
}

func TestHandleTaskStatus_UpdatesExistingTracker(t *testing.T) {
	r := require.New(t)

	s := NewTaskTrackerSet()

	// First update
	payload := map[string]any{
		"id":          "b3",
		"description": "Compiling Señor Chang's grading app",
		"status":      "running",
		"outputTail":  []string{"building..."},
	}
	data, _ := json.Marshal(payload)
	s.HandleStatus(string(data))

	// Second update — same ID, new output
	payload["outputTail"] = []string{"building...", "linking...", "done!"}
	data, _ = json.Marshal(payload)
	s.HandleStatus(string(data))

	// Still one tracker, not two.
	r.Equal(1, s.Len())
	r.Len(s.order, 1)

	tt := s.trackers["b3"]
	r.Equal([]string{"building...", "linking...", "done!"}, tt.outputTail)
}

func TestHandleTaskStatus_TerminalFinalizesToOutput(t *testing.T) {
	r := require.New(t)

	s := NewTaskTrackerSet()

	// Start running
	payload := map[string]any{
		"id":          "b7",
		"description": "Troy and Abed's morning show build",
		"status":      "running",
		"outputTail":  []string{"compiling..."},
	}
	data, _ := json.Marshal(payload)
	summary, terminal := s.HandleStatus(string(data))
	r.Empty(summary)
	r.False(terminal)
	r.Equal(1, s.Len())

	// Complete
	payload["status"] = "completed"
	payload["duration"] = "4.2s"
	data, _ = json.Marshal(payload)
	summary, terminal = s.HandleStatus(string(data))

	// Tracker removed, summary line returned.
	r.True(terminal)
	r.Equal(0, s.Len())
	r.Len(s.order, 0)
	r.NotEmpty(summary)
	r.Contains(summary, "Troy and Abed's morning show build")
	r.Contains(summary, "b7")
	r.Contains(summary, "4.2s")
}

func TestHandleTaskStatus_MultipleTrackers(t *testing.T) {
	r := require.New(t)

	s := NewTaskTrackerSet()

	for _, id := range []string{"b1", "b2", "a3"} {
		payload := map[string]any{
			"id":          id,
			"description": "Task " + id,
			"status":      "running",
			"outputTail":  []string{},
		}
		data, _ := json.Marshal(payload)
		s.HandleStatus(string(data))
	}

	r.Equal(3, s.Len())
	r.Equal([]string{"b1", "b2", "a3"}, s.order)
}

func TestRenderTaskTrackers_Empty(t *testing.T) {
	r := require.New(t)
	s := NewTaskTrackerSet()
	r.Empty(s.Render("⠋", 120))
}

func TestRenderTaskTrackers_WithContent(t *testing.T) {
	r := require.New(t)

	s := &TaskTrackerSet{
		trackers: map[string]*taskTracker{
			"b3": {
				taskID:      "b3",
				description: "Running tests",
				status:      "running",
				outputTail:  []string{"PASS TestFoo", "FAIL TestBar"},
			},
		},
		order: []string{"b3"},
	}

	rendered := s.Render("⠋", 120)
	r.Contains(rendered, "Running tests")
	r.Contains(rendered, "b3")
	r.Contains(rendered, "running")
	r.Contains(rendered, "PASS TestFoo")
	r.Contains(rendered, "FAIL TestBar")
}

func TestTaskTrackerHeight(t *testing.T) {
	r := require.New(t)

	s := &TaskTrackerSet{
		trackers: map[string]*taskTracker{
			"b1": {
				taskID:     "b1",
				outputTail: []string{"line1", "line2"},
			},
			"b2": {
				taskID:     "b2",
				outputTail: []string{"a", "b", "c"},
			},
		},
		order: []string{"b1", "b2"},
	}

	// b1: 1 header + 2 output = 3
	// b2: 1 header + 3 output = 4
	// Total: 7
	r.Equal(7, s.Height())
}

func TestHandleTaskStatus_InvalidJSON(t *testing.T) {
	r := require.New(t)

	s := NewTaskTrackerSet()

	// Should not panic on garbage input.
	summary, terminal := s.HandleStatus("not json")
	r.Empty(summary)
	r.False(terminal)
	r.Equal(0, s.Len())
}
