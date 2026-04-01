// Package task manages background tasks (bash commands and sub-agents).
package task

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
)

// Manager handles background task lifecycle.
type Manager struct {
	mu     sync.RWMutex
	tasks  map[string]*types.Task
	agents map[string]*types.SubAgent
}

// NewManager creates a task manager.
func NewManager() *Manager {
	return &Manager{
		tasks:  make(map[string]*types.Task),
		agents: make(map[string]*types.SubAgent),
	}
}

// CreateBashTask creates and starts a background bash command.
func (m *Manager) CreateBashTask(sessionID, description, command, cwd string, timeout int) (*types.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	taskID := generateTaskID("bash")
	ctx, cancel := context.WithCancel(context.Background())

	task := &types.Task{
		ID:          taskID,
		Type:        types.TaskTypeBash,
		Status:      types.TaskStatusPending,
		Description: description,
		StartTime:   time.Now(),
		SessionID:   sessionID,
		Command:     command,
		CWD:         cwd,
		Timeout:     timeout,
		Cancel:      cancel,
		Metadata:    make(map[string]interface{}),
	}

	m.tasks[taskID] = task

	// Start task in background
	go m.runBashTask(ctx, task)

	return task, nil
}

// runBashTask executes a bash command in the background.
func (m *Manager) runBashTask(ctx context.Context, task *types.Task) {
	m.updateTaskStatus(task.ID, types.TaskStatusRunning)

	// Apply timeout if specified
	if task.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(task.Timeout)*time.Second)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "bash", "-c", task.Command)
	cmd.Dir = task.CWD

	output, err := cmd.CombinedOutput()
	now := time.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	task.EndTime = &now
	task.Output = string(output)

	if err != nil {
		task.Status = types.TaskStatusFailed
		task.Error = err.Error()
		if exitErr, ok := err.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			task.ExitCode = &code
		}
	} else {
		task.Status = types.TaskStatusCompleted
		code := 0
		task.ExitCode = &code
	}
}

// CreateAgent creates a sub-agent task.
func (m *Manager) CreateAgent(parentSessionID, agentType, description, prompt, model string, tools, disallowedTools []string, maxTurns int) (*types.SubAgent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	agentID := generateTaskID("agent")
	sessionID := generateSessionID()

	agent := &types.SubAgent{
		ID:              agentID,
		SessionID:       sessionID,
		ParentSessionID: parentSessionID,
		Type:            agentType,
		Description:     description,
		Status:          types.TaskStatusPending,
		StartTime:       time.Now(),
		Prompt:          prompt,
		Model:           model,
		Tools:           tools,
		DisallowedTools: disallowedTools,
		MaxTurns:        maxTurns,
		TurnCount:       0,
		Metadata:        make(map[string]interface{}),
		Messages:        []types.ChatMessage{},
	}

	m.agents[agentID] = agent
	return agent, nil
}

// GetTask retrieves a task by ID.
func (m *Manager) GetTask(taskID string) (*types.Task, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	task, ok := m.tasks[taskID]
	return task, ok
}

// GetAgent retrieves a sub-agent by ID.
func (m *Manager) GetAgent(agentID string) (*types.SubAgent, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	agent, ok := m.agents[agentID]
	return agent, ok
}

// ListTasks returns all tasks for a session.
func (m *Manager) ListTasks(sessionID string) []*types.Task {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tasks := make([]*types.Task, 0)
	for _, task := range m.tasks {
		if task.SessionID == sessionID {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

// ListAgents returns all sub-agents for a session.
func (m *Manager) ListAgents(parentSessionID string) []*types.SubAgent {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agents := make([]*types.SubAgent, 0)
	for _, agent := range m.agents {
		if agent.ParentSessionID == parentSessionID {
			agents = append(agents, agent)
		}
	}
	return agents
}

// StopTask stops a running task.
func (m *Manager) StopTask(taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	task, ok := m.tasks[taskID]
	if !ok {
		return fmt.Errorf("task not found: %s", taskID)
	}

	if task.Status.IsTerminal() {
		return fmt.Errorf("task already terminated: %s", task.Status)
	}

	if task.Cancel != nil {
		task.Cancel()
	}

	now := time.Now()
	task.Status = types.TaskStatusKilled
	task.EndTime = &now

	return nil
}

// StopAgent stops a running sub-agent.
func (m *Manager) StopAgent(agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agent, ok := m.agents[agentID]
	if !ok {
		return fmt.Errorf("agent not found: %s", agentID)
	}

	if agent.Status.IsTerminal() {
		return fmt.Errorf("agent already terminated: %s", agent.Status)
	}

	now := time.Now()
	agent.Status = types.TaskStatusKilled
	agent.EndTime = &now

	return nil
}

// updateTaskStatus updates a task's status (caller must hold lock).
func (m *Manager) updateTaskStatus(taskID string, status types.TaskStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if task, ok := m.tasks[taskID]; ok {
		task.Status = status
		if status == types.TaskStatusRunning && task.StartTime.IsZero() {
			task.StartTime = time.Now()
		}
	}
}

// generateTaskID creates a unique task ID with a type prefix.
func generateTaskID(taskType string) string {
	prefix := "x"
	switch taskType {
	case "bash":
		prefix = "b"
	case "agent":
		prefix = "a"
	}
	return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
}

// generateSessionID creates a unique session ID for sub-agents.
func generateSessionID() string {
	return fmt.Sprintf("subagent-%d", time.Now().UnixNano())
}
