package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jelmersnoeck/forge/internal/runtime/task"
	"github.com/jelmersnoeck/forge/internal/types"
)

// taskStatusBroadcaster periodically emits task_status events for all
// non-terminal tasks so the CLI can show live-updating progress with
// rolling output, even while the LLM is thinking or executing other tools.
//
// Runs every second. Stops when ctx is cancelled.
func (w *Worker) taskStatusBroadcaster(ctx context.Context, mgr *task.Manager) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.broadcastTaskStatuses(mgr)
		}
	}
}

// broadcastTaskStatuses emits a task_status event for every task and
// sub-agent owned by this session that the CLI is likely tracking (i.e.
// non-terminal, or recently-completed ones the CLI hasn't finalized yet).
func (w *Worker) broadcastTaskStatuses(mgr *task.Manager) {
	for _, t := range mgr.ListTasks(w.sessionID) {
		if t.Status.IsTerminal() {
			continue
		}
		w.emitTaskStatusEvent(t.ID, t.Description, string(t.Status), t.Output, t.StartTime, t.EndTime)
	}

	for _, a := range mgr.ListAgents(w.sessionID) {
		if a.Status.IsTerminal() {
			continue
		}
		w.emitTaskStatusEvent(a.ID, a.Description, string(a.Status), a.Output, a.StartTime, a.EndTime)
	}
}

// emitTaskStatusEvent publishes a single task_status event through the hub.
func (w *Worker) emitTaskStatusEvent(id, description, status, output string, startTime time.Time, endTime *time.Time) {
	var tail []string
	if output != "" {
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= 0 && len(tail) < 5; i-- {
			if strings.TrimSpace(lines[i]) != "" {
				tail = append([]string{lines[i]}, tail...)
			}
		}
	}

	payload := map[string]any{
		"id":          id,
		"description": description,
		"status":      status,
		"outputTail":  tail,
		"startTime":   startTime.Format("2006-01-02 15:04:05"),
	}
	if endTime != nil {
		payload["duration"] = endTime.Sub(startTime).String()
	}

	data, _ := json.Marshal(payload)
	w.hub.PublishEvent(types.OutboundEvent{
		ID:        uuid.New().String(),
		SessionID: w.sessionID,
		Type:      "task_status",
		Content:   string(data),
		Timestamp: time.Now().UnixMilli(),
	})
}
