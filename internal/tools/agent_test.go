package tools

import (
	"encoding/json"
	"testing"

	"github.com/jelmersnoeck/forge/internal/runtime/task"
	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestHandleAgentGet_EmitsTaskStatus(t *testing.T) {
	r := require.New(t)

	mgr := task.NewManager()
	defer mgr.Stop()
	oldMgr := taskMgr
	taskMgr = mgr
	defer func() { taskMgr = oldMgr }()

	agent, err := mgr.CreateAgent(
		"session-study-group",
		"reviewer",
		"Reviewing Jeff's closing argument",
		"Review the code",
		"",
		nil, nil, 0,
	)
	r.NoError(err)

	var events []types.OutboundEvent
	ctx := types.ToolContext{
		SessionID: "session-study-group",
		Emit: func(ev types.OutboundEvent) {
			events = append(events, ev)
		},
	}

	result, err := handleAgentGet(map[string]any{"agent_id": agent.ID}, ctx)
	r.NoError(err)
	r.False(result.IsError)

	// Should have emitted exactly one task_status event.
	r.Len(events, 1)
	ev := events[0]
	r.Equal("task_status", ev.Type)

	var payload struct {
		ID          string `json:"id"`
		Description string `json:"description"`
		Status      string `json:"status"`
	}
	r.NoError(json.Unmarshal([]byte(ev.Content), &payload))
	r.Equal(agent.ID, payload.ID)
	r.Equal("Reviewing Jeff's closing argument", payload.Description)
	r.Equal("pending", payload.Status)
}
