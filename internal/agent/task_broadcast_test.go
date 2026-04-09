package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jelmersnoeck/forge/internal/runtime/task"
	"github.com/stretchr/testify/require"
)

func TestBroadcastTaskStatuses(t *testing.T) {
	r := require.New(t)

	mgr := task.NewManager()
	defer mgr.Stop()

	hub := NewHub()

	w := &Worker{
		hub:       hub,
		sessionID: "session-paintball",
	}

	// Create a long-running task so it's still running when we broadcast.
	tk, err := mgr.CreateBashTask("session-paintball", "Paintball simulation", "sleep 30", t.TempDir(), 60)
	r.NoError(err)

	// Wait for status to become running via race-safe snapshot.
	deadline := time.Now().Add(2 * time.Second)
	for {
		snap, _ := mgr.GetTaskSnapshot(tk.ID)
		if string(snap.Status) == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("task did not start within 2s")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Subscribe, broadcast, collect.
	ch, unsub := hub.Subscribe()
	defer unsub()

	w.broadcastTaskStatuses(mgr)

	select {
	case ev := <-ch:
		r.Equal("task_status", ev.Type)
		r.Equal("session-paintball", ev.SessionID)

		var payload struct {
			ID          string `json:"id"`
			Description string `json:"description"`
			Status      string `json:"status"`
		}
		r.NoError(json.Unmarshal([]byte(ev.Content), &payload))
		r.Equal(tk.ID, payload.ID)
		r.Equal("Paintball simulation", payload.Description)
		r.Equal("running", payload.Status)
	case <-time.After(time.Second):
		t.Fatal("expected a task_status event")
	}

	// Stop the task so it doesn't linger.
	r.NoError(mgr.StopTask(tk.ID))
}

func TestBroadcastTaskStatuses_SkipsTerminal(t *testing.T) {
	r := require.New(t)

	mgr := task.NewManager()
	defer mgr.Stop()

	hub := NewHub()

	w := &Worker{
		hub:       hub,
		sessionID: "session-greendale",
	}

	// Create a task that finishes quickly.
	_, err := mgr.CreateBashTask("session-greendale", "Quick task", "echo 'pop pop'", t.TempDir(), 10)
	r.NoError(err)

	// Wait for it to complete via race-safe snapshots.
	deadline := time.Now().Add(5 * time.Second)
	for {
		snaps := mgr.ListTaskSnapshots("session-greendale")
		allDone := true
		for _, snap := range snaps {
			if !snap.Status.IsTerminal() {
				allDone = false
			}
		}
		if allDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("task did not complete within 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Subscribe, broadcast, check for silence.
	ch, unsub := hub.Subscribe()
	defer unsub()

	w.broadcastTaskStatuses(mgr)

	select {
	case ev := <-ch:
		t.Fatalf("expected no events for terminal tasks, got: %s", ev.Type)
	case <-time.After(100 * time.Millisecond):
		// Good — no events.
	}
}

func TestEmitTaskStatusEvent_OutputTail(t *testing.T) {
	r := require.New(t)

	hub := NewHub()
	w := &Worker{
		hub:       hub,
		sessionID: "session-abed",
	}

	ch, unsub := hub.Subscribe()
	defer unsub()

	output := strings.Join([]string{
		"line1", "line2", "line3", "line4", "line5",
		"line6", "line7", "",
	}, "\n")
	now := time.Now()
	w.emitTaskStatusEvent("b42", "Inspector Spacetime", "running", output, now, nil)

	select {
	case ev := <-ch:
		var payload struct {
			OutputTail []string `json:"outputTail"`
		}
		r.NoError(json.Unmarshal([]byte(ev.Content), &payload))
		r.Len(payload.OutputTail, 5)
		r.Equal("line3", payload.OutputTail[0])
		r.Equal("line7", payload.OutputTail[4])
	case <-time.After(time.Second):
		t.Fatal("expected a task_status event")
	}
}
