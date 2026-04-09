package tools

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jelmersnoeck/forge/internal/runtime/task"
	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestHandleTaskGet_EmitsTaskStatus(t *testing.T) {
	r := require.New(t)

	mgr := task.NewManager()
	defer mgr.Stop()
	oldMgr := taskMgr
	taskMgr = mgr
	defer func() { taskMgr = oldMgr }()

	tk, err := mgr.CreateBashTask("session-greendale", "Running paintball sim", "echo 'Troy Barnes wins'", t.TempDir(), 10)
	r.NoError(err)

	// Wait for the task to finish.
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, ok := mgr.GetTask(tk.ID)
		r.True(ok)
		if got.Status.IsTerminal() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("task did not complete within 5s")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Collect emitted events.
	var events []types.OutboundEvent
	ctx := types.ToolContext{
		SessionID: "session-greendale",
		Emit: func(ev types.OutboundEvent) {
			events = append(events, ev)
		},
	}

	result, err := handleTaskGet(map[string]any{"task_id": tk.ID}, ctx)
	r.NoError(err)
	r.False(result.IsError)

	// Should have emitted exactly one task_status event.
	r.Len(events, 1)
	ev := events[0]
	r.Equal("task_status", ev.Type)
	r.Equal("session-greendale", ev.SessionID)

	var payload struct {
		ID          string   `json:"id"`
		Description string   `json:"description"`
		Status      string   `json:"status"`
		OutputTail  []string `json:"outputTail"`
		Duration    string   `json:"duration"`
	}
	r.NoError(json.Unmarshal([]byte(ev.Content), &payload))
	r.Equal(tk.ID, payload.ID)
	r.Equal("Running paintball sim", payload.Description)
	r.Equal("completed", payload.Status)
	r.NotEmpty(payload.Duration)
	// Output should contain our echo.
	r.NotEmpty(payload.OutputTail)
	r.Contains(payload.OutputTail[0], "Troy Barnes wins")
}

func TestEmitTaskStatus_TailsTruncation(t *testing.T) {
	r := require.New(t)

	var events []types.OutboundEvent
	ctx := types.ToolContext{
		SessionID: "session-hawthorne",
		Emit: func(ev types.OutboundEvent) {
			events = append(events, ev)
		},
	}

	output := "line1\nline2\nline3\nline4\nline5\nline6\nline7\n"
	now := time.Now()
	emitTaskStatus(ctx, "b42", "Dean Pelton's Report", "running", output, now, nil)

	r.Len(events, 1)

	var payload struct {
		OutputTail []string `json:"outputTail"`
		Duration   string   `json:"duration"`
	}
	r.NoError(json.Unmarshal([]byte(events[0].Content), &payload))

	// Should have at most 5 lines.
	r.Len(payload.OutputTail, 5)
	r.Equal("line3", payload.OutputTail[0])
	r.Equal("line7", payload.OutputTail[4])

	// No endTime => no duration.
	r.Empty(payload.Duration)
}

func TestEmitTaskStatus_EmptyOutput(t *testing.T) {
	r := require.New(t)

	var events []types.OutboundEvent
	ctx := types.ToolContext{
		SessionID: "session-study-room-f",
		Emit: func(ev types.OutboundEvent) {
			events = append(events, ev)
		},
	}

	now := time.Now()
	emitTaskStatus(ctx, "b1", "Silence", "running", "", now, nil)

	r.Len(events, 1)

	var payload struct {
		OutputTail []string `json:"outputTail"`
	}
	r.NoError(json.Unmarshal([]byte(events[0].Content), &payload))
	r.Empty(payload.OutputTail)
}
