package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHandleTaskStatus_CreatesTracker(t *testing.T) {
	r := require.New(t)

	m := &model{
		taskTrackers:     make(map[string]*taskTracker),
		taskTrackerOrder: nil,
		width:            120,
	}

	payload := map[string]any{
		"id":          "b3",
		"description": "Running tests at Greendale",
		"status":      "running",
		"outputTail":  []string{"PASS TestPaintball", "PASS TestDarkTimeline"},
	}
	data, _ := json.Marshal(payload)
	m.handleTaskStatus(string(data))

	r.Len(m.taskTrackers, 1)
	tt := m.taskTrackers["b3"]
	r.NotNil(tt)
	r.Equal("b3", tt.taskID)
	r.Equal("Running tests at Greendale", tt.description)
	r.Equal("running", tt.status)
	r.Equal([]string{"PASS TestPaintball", "PASS TestDarkTimeline"}, tt.outputTail)
	r.Equal([]string{"b3"}, m.taskTrackerOrder)
}

func TestHandleTaskStatus_UpdatesExistingTracker(t *testing.T) {
	r := require.New(t)

	m := &model{
		taskTrackers:     make(map[string]*taskTracker),
		taskTrackerOrder: nil,
		width:            120,
	}

	// First update
	payload := map[string]any{
		"id":          "b3",
		"description": "Compiling Señor Chang's grading app",
		"status":      "running",
		"outputTail":  []string{"building..."},
	}
	data, _ := json.Marshal(payload)
	m.handleTaskStatus(string(data))

	// Second update — same ID, new output
	payload["outputTail"] = []string{"building...", "linking...", "done!"}
	data, _ = json.Marshal(payload)
	m.handleTaskStatus(string(data))

	// Still one tracker, not two.
	r.Len(m.taskTrackers, 1)
	r.Len(m.taskTrackerOrder, 1)

	tt := m.taskTrackers["b3"]
	r.Equal([]string{"building...", "linking...", "done!"}, tt.outputTail)
}

func TestHandleTaskStatus_TerminalFinalizesToOutput(t *testing.T) {
	r := require.New(t)

	m := &model{
		taskTrackers:     make(map[string]*taskTracker),
		taskTrackerOrder: nil,
		output:           []string{},
		width:            120,
	}

	// Start running
	payload := map[string]any{
		"id":          "b7",
		"description": "Troy and Abed's morning show build",
		"status":      "running",
		"outputTail":  []string{"compiling..."},
	}
	data, _ := json.Marshal(payload)
	m.handleTaskStatus(string(data))
	r.Len(m.taskTrackers, 1)

	// Complete
	payload["status"] = "completed"
	payload["duration"] = "4.2s"
	data, _ = json.Marshal(payload)
	m.handleTaskStatus(string(data))

	// Tracker removed, summary line appended to output.
	r.Len(m.taskTrackers, 0)
	r.Len(m.taskTrackerOrder, 0)
	r.NotEmpty(m.output)
	// The last output line should mention the description.
	last := m.output[len(m.output)-1]
	r.Contains(last, "Troy and Abed's morning show build")
	r.Contains(last, "b7")
	r.Contains(last, "4.2s")
}

func TestHandleTaskStatus_MultipleTrackers(t *testing.T) {
	r := require.New(t)

	m := &model{
		taskTrackers:     make(map[string]*taskTracker),
		taskTrackerOrder: nil,
		width:            120,
	}

	for _, id := range []string{"b1", "b2", "a3"} {
		payload := map[string]any{
			"id":          id,
			"description": "Task " + id,
			"status":      "running",
			"outputTail":  []string{},
		}
		data, _ := json.Marshal(payload)
		m.handleTaskStatus(string(data))
	}

	r.Len(m.taskTrackers, 3)
	r.Equal([]string{"b1", "b2", "a3"}, m.taskTrackerOrder)
}

func TestRenderTaskTrackers_Empty(t *testing.T) {
	r := require.New(t)

	m := model{
		taskTrackers:     make(map[string]*taskTracker),
		taskTrackerOrder: nil,
		width:            120,
	}

	r.Empty(m.renderTaskTrackers())
}

func TestRenderTaskTrackers_WithContent(t *testing.T) {
	r := require.New(t)

	m := model{
		taskTrackers: map[string]*taskTracker{
			"b3": {
				taskID:      "b3",
				description: "Running tests",
				status:      "running",
				outputTail:  []string{"PASS TestFoo", "FAIL TestBar"},
			},
		},
		taskTrackerOrder: []string{"b3"},
		width:            120,
		spinnerFrame:     0,
	}

	rendered := m.renderTaskTrackers()
	r.Contains(rendered, "Running tests")
	r.Contains(rendered, "b3")
	r.Contains(rendered, "running")
	r.Contains(rendered, "PASS TestFoo")
	r.Contains(rendered, "FAIL TestBar")
}

func TestTaskTrackerHeight(t *testing.T) {
	r := require.New(t)

	m := model{
		taskTrackers: map[string]*taskTracker{
			"b1": {
				taskID:     "b1",
				outputTail: []string{"line1", "line2"},
			},
			"b2": {
				taskID:     "b2",
				outputTail: []string{"a", "b", "c"},
			},
		},
		taskTrackerOrder: []string{"b1", "b2"},
	}

	// b1: 1 header + 2 output = 3
	// b2: 1 header + 3 output = 4
	// Total: 7
	r.Equal(7, m.taskTrackerHeight())
}

func TestHandleTaskStatus_InvalidJSON(t *testing.T) {
	r := require.New(t)

	m := &model{
		taskTrackers:     make(map[string]*taskTracker),
		taskTrackerOrder: nil,
	}

	// Should not panic on garbage input.
	m.handleTaskStatus("not json")
	r.Empty(m.taskTrackers)
}
