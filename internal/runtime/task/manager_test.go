package task

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestManager_CreateBashTask(t *testing.T) {
	r := require.New(t)

	m := NewManager()
	task, err := m.CreateBashTask("session1", "Echo test", "echo 'hello world'", "/tmp", 0)
	r.NoError(err)
	r.NotNil(task)
	r.Equal("session1", task.SessionID)
	r.Equal("Echo test", task.Description)
	r.Equal("echo 'hello world'", task.Command)
	r.True(task.ID != "")

	// Wait for task to complete
	time.Sleep(100 * time.Millisecond)

	retrieved, found := m.GetTask(task.ID)
	r.True(found)
	r.Equal(types.TaskStatusCompleted, retrieved.Status)
	r.Contains(retrieved.Output, "hello world")
	r.NotNil(retrieved.ExitCode)
	r.Equal(0, *retrieved.ExitCode)
}

func TestManager_CreateBashTask_Failure(t *testing.T) {
	r := require.New(t)

	m := NewManager()
	task, err := m.CreateBashTask("session1", "Failing command", "exit 42", "/tmp", 0)
	r.NoError(err)

	// Wait for task to fail
	time.Sleep(100 * time.Millisecond)

	retrieved, found := m.GetTask(task.ID)
	r.True(found)
	r.Equal(types.TaskStatusFailed, retrieved.Status)
	r.NotNil(retrieved.ExitCode)
	r.Equal(42, *retrieved.ExitCode)
	r.NotEmpty(retrieved.Error)
}

func TestManager_CreateBashTask_Timeout(t *testing.T) {
	r := require.New(t)

	m := NewManager()
	task, err := m.CreateBashTask("session1", "Sleep forever", "sleep 10", "/tmp", 1)
	r.NoError(err)

	// Wait for timeout
	time.Sleep(1500 * time.Millisecond)

	retrieved, found := m.GetTask(task.ID)
	r.True(found)
	r.Equal(types.TaskStatusFailed, retrieved.Status)
}

func TestManager_ListTasks(t *testing.T) {
	r := require.New(t)

	m := NewManager()
	_, err := m.CreateBashTask("session1", "Task 1", "echo 1", "/tmp", 0)
	r.NoError(err)
	_, err = m.CreateBashTask("session1", "Task 2", "echo 2", "/tmp", 0)
	r.NoError(err)
	_, err = m.CreateBashTask("session2", "Task 3", "echo 3", "/tmp", 0)
	r.NoError(err)

	tasks := m.ListTasks("session1")
	r.Len(tasks, 2)

	tasks = m.ListTasks("session2")
	r.Len(tasks, 1)
}

func TestManager_StopTask(t *testing.T) {
	r := require.New(t)

	m := NewManager()
	task, err := m.CreateBashTask("session1", "Long sleep", "sleep 100", "/tmp", 0)
	r.NoError(err)

	// Wait for task to start
	time.Sleep(100 * time.Millisecond)

	err = m.StopTask(task.ID)
	r.NoError(err)

	// Let the background goroutine finish (it will see terminal status and bail).
	time.Sleep(200 * time.Millisecond)

	retrieved, found := m.GetTask(task.ID)
	r.True(found)
	r.Equal(types.TaskStatusKilled, retrieved.Status)
	r.NotNil(retrieved.EndTime)
}

func TestManager_StreamingOutput(t *testing.T) {
	r := require.New(t)

	m := NewManager()
	// This command prints lines with 200ms gaps. After 800ms we should see
	// partial output before the command finishes at ~1s.
	cmd := `for i in 1 2 3 4 5; do echo "line$i"; sleep 0.2; done`
	task, err := m.CreateBashTask("session-streaming", "Streaming test", cmd, "/tmp", 10)
	r.NoError(err)

	// Wait ~800ms — GetTaskOutput reads live buffer contents.
	time.Sleep(800 * time.Millisecond)

	snap, found := m.GetTaskSnapshot(task.ID)
	r.True(found)
	r.Equal(types.TaskStatusRunning, snap.Status, "task should still be running at 800ms")
	r.Contains(snap.Output, "line1", "partial output should be visible while running")

	// Wait for completion.
	deadline := time.Now().Add(5 * time.Second)
	for {
		snap, _ = m.GetTaskSnapshot(task.ID)
		if snap.Status.IsTerminal() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("task did not finish in 5s")
		}
		time.Sleep(50 * time.Millisecond)
	}

	r.Equal(types.TaskStatusCompleted, snap.Status)
	r.Contains(snap.Output, "line5")
}

func TestManager_CreateAgent(t *testing.T) {
	r := require.New(t)

	m := NewManager()
	agent, err := m.CreateAgent(
		"parent-session",
		"test-agent",
		"Test agent",
		"You are a test agent",
		"claude-3-5-sonnet-20241022",
		[]string{"Read", "Write"},
		[]string{"Bash"},
		10,
	)
	r.NoError(err)
	r.NotNil(agent)
	r.Equal("parent-session", agent.ParentSessionID)
	r.Equal("test-agent", agent.Type)
	r.Equal("Test agent", agent.Description)
	r.Equal(types.TaskStatusPending, agent.Status)
	r.Len(agent.Tools, 2)
	r.Len(agent.DisallowedTools, 1)
	r.Equal(10, agent.MaxTurns)
	r.Equal(0, agent.TurnCount)
}

func TestManager_ListAgents(t *testing.T) {
	r := require.New(t)

	m := NewManager()
	_, err := m.CreateAgent("session1", "agent1", "Agent 1", "prompt1", "", nil, nil, 0)
	r.NoError(err)
	_, err = m.CreateAgent("session1", "agent2", "Agent 2", "prompt2", "", nil, nil, 0)
	r.NoError(err)
	_, err = m.CreateAgent("session2", "agent3", "Agent 3", "prompt3", "", nil, nil, 0)
	r.NoError(err)

	agents := m.ListAgents("session1")
	r.Len(agents, 2)

	agents = m.ListAgents("session2")
	r.Len(agents, 1)
}

func TestManager_StopAgent(t *testing.T) {
	r := require.New(t)

	m := NewManager()
	agent, err := m.CreateAgent("session1", "test", "Test", "prompt", "", nil, nil, 0)
	r.NoError(err)

	err = m.StopAgent(agent.ID)
	r.NoError(err)

	retrieved, found := m.GetAgent(agent.ID)
	r.True(found)
	r.Equal(types.TaskStatusKilled, retrieved.Status)
	r.NotNil(retrieved.EndTime)
}

func TestManager_StopAgent_NotFound(t *testing.T) {
	r := require.New(t)

	m := NewManager()
	err := m.StopAgent("nonexistent")
	r.Error(err)
	r.Contains(err.Error(), "not found")
}

func TestManager_StopAgent_AlreadyTerminated(t *testing.T) {
	r := require.New(t)

	m := NewManager()
	agent, err := m.CreateAgent("session1", "test", "Test", "prompt", "", nil, nil, 0)
	r.NoError(err)

	err = m.StopAgent(agent.ID)
	r.NoError(err)

	// Try to stop again
	err = m.StopAgent(agent.ID)
	r.Error(err)
	r.Contains(err.Error(), "already terminated")
}

func TestTaskStatus_IsTerminal(t *testing.T) {
	tests := map[string]struct {
		status   types.TaskStatus
		terminal bool
	}{
		"pending":   {types.TaskStatusPending, false},
		"running":   {types.TaskStatusRunning, false},
		"completed": {types.TaskStatusCompleted, true},
		"failed":    {types.TaskStatusFailed, true},
		"killed":    {types.TaskStatusKilled, true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.terminal, tc.status.IsTerminal())
		})
	}
}

func TestManager_RunAgent_NoRunner(t *testing.T) {
	r := require.New(t)

	m := NewManager()
	defer m.Stop()
	agent, err := m.CreateAgent("session1", "test", "Test", "prompt", "", nil, nil, 0)
	r.NoError(err)

	err = m.RunAgent(agent.ID)
	r.Error(err)
	r.Contains(err.Error(), "no agent runner configured")
}

func TestManager_RunAgent_NotFound(t *testing.T) {
	r := require.New(t)

	m := NewManager()
	defer m.Stop()
	err := m.RunAgent("nonexistent")
	r.Error(err)
	r.Contains(err.Error(), "not found")
}

func TestManager_RunAgent_Success(t *testing.T) {
	r := require.New(t)

	m := NewManager()
	defer m.Stop()

	m.SetAgentRunner(func(ctx context.Context, agent *types.SubAgent) error {
		agent.Output = "Greendale is where I belong"
		return nil
	})

	agent, err := m.CreateAgent("session1", "test_agent", "Dean Pelton's helper", "Tell me about Greendale Community College", "", nil, nil, 10)
	r.NoError(err)

	err = m.RunAgent(agent.ID)
	r.NoError(err)

	// Wait for goroutine
	time.Sleep(100 * time.Millisecond)

	retrieved, found := m.GetAgent(agent.ID)
	r.True(found)
	r.Equal(types.TaskStatusCompleted, retrieved.Status)
	r.Equal("Greendale is where I belong", retrieved.Output)
	r.NotNil(retrieved.EndTime)
	r.Empty(retrieved.Error)
}

func TestManager_RunAgent_Failure(t *testing.T) {
	r := require.New(t)

	m := NewManager()
	defer m.Stop()

	m.SetAgentRunner(func(ctx context.Context, agent *types.SubAgent) error {
		agent.Output = "partial output from Troy Barnes"
		return fmt.Errorf("streets ahead of what I can handle")
	})

	agent, err := m.CreateAgent("session1", "test_agent", "Failing agent", "Do the impossible", "", nil, nil, 0)
	r.NoError(err)

	err = m.RunAgent(agent.ID)
	r.NoError(err)

	time.Sleep(100 * time.Millisecond)

	retrieved, found := m.GetAgent(agent.ID)
	r.True(found)
	r.Equal(types.TaskStatusFailed, retrieved.Status)
	r.Contains(retrieved.Error, "streets ahead")
	r.Equal("partial output from Troy Barnes", retrieved.Output)
}

func TestManager_RunAgent_Cancellation(t *testing.T) {
	r := require.New(t)

	m := NewManager()
	defer m.Stop()

	started := make(chan struct{})
	m.SetAgentRunner(func(ctx context.Context, agent *types.SubAgent) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})

	agent, err := m.CreateAgent("session1", "test_agent", "Cancellable agent", "Wait forever", "", nil, nil, 0)
	r.NoError(err)

	err = m.RunAgent(agent.ID)
	r.NoError(err)

	// Wait for runner to start
	<-started

	err = m.StopAgent(agent.ID)
	r.NoError(err)

	time.Sleep(100 * time.Millisecond)

	retrieved, found := m.GetAgent(agent.ID)
	r.True(found)
	r.Equal(types.TaskStatusKilled, retrieved.Status)
}
