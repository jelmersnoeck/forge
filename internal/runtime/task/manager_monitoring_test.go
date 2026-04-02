package task

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManager_MonitorLongRunningTask(t *testing.T) {
	r := require.New(t)
	m := NewManager()
	defer m.Stop()

	// Create a long-running task without timeout
	task, err := m.CreateBashTask("test-session", "long sleep", "sleep 10", "/tmp", 0)
	r.NoError(err)
	r.NotNil(task)

	// Wait for initial monitoring cycle (30s is too long for test)
	// We'll manually trigger the check instead
	time.Sleep(100 * time.Millisecond) // Let task start

	// Manually set start time to simulate long-running task
	m.mu.Lock()
	task.StartTime = time.Now().Add(-6 * time.Minute)
	m.mu.Unlock()

	// Trigger monitoring check
	m.checkLongRunningTasks()

	// Check if warning was sent
	select {
	case warning := <-m.GetWarnings():
		r.Contains(warning, task.ID)
		r.Contains(warning, "running for")
		r.Contains(warning, "without a timeout")
	case <-time.After(1 * time.Second):
		t.Fatal("expected warning for long-running task")
	}

	// Stop the task
	err = m.StopTask(task.ID)
	r.NoError(err)
}

func TestManager_MonitorApproachingTimeout(t *testing.T) {
	r := require.New(t)
	m := NewManager()
	defer m.Stop()

	// Create a task with a 10 second timeout
	task, err := m.CreateBashTask("test-session", "timed sleep", "sleep 10", "/tmp", 10)
	r.NoError(err)
	r.NotNil(task)

	time.Sleep(100 * time.Millisecond) // Let task start

	// Manually set start time to simulate 90% elapsed (9 seconds of 10 second timeout)
	m.mu.Lock()
	task.StartTime = time.Now().Add(-9 * time.Second)
	m.mu.Unlock()

	// Trigger monitoring check
	m.checkLongRunningTasks()

	// Check if warning was sent
	select {
	case warning := <-m.GetWarnings():
		r.Contains(warning, task.ID)
		r.Contains(warning, "approaching timeout")
	case <-time.After(1 * time.Second):
		t.Fatal("expected warning for approaching timeout")
	}

	// Stop the task
	err = m.StopTask(task.ID)
	r.NoError(err)
}

func TestManager_TimeoutErrorMessage(t *testing.T) {
	r := require.New(t)
	m := NewManager()
	defer m.Stop()

	// Create a task that will timeout (1 second timeout for 5 second sleep)
	task, err := m.CreateBashTask("test-session", "timeout test", "sleep 5", "/tmp", 1)
	r.NoError(err)
	r.NotNil(task)

	// Wait for task to complete/timeout
	time.Sleep(2 * time.Second)

	// Check task status
	fetchedTask, found := m.GetTask(task.ID)
	r.True(found)
	r.Equal("failed", string(fetchedTask.Status))
	r.Contains(fetchedTask.Error, "timed out")
	r.Contains(fetchedTask.Output, "TIMEOUT")
	r.Contains(fetchedTask.Output, "exceeded 1 second limit")
}
