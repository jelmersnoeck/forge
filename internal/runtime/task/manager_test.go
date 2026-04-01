package task

import (
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

	retrieved, found := m.GetTask(task.ID)
	r.True(found)
	r.Equal(types.TaskStatusKilled, retrieved.Status)
	r.NotNil(retrieved.EndTime)
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
