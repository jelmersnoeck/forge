// Package task manages background tasks (bash commands and sub-agents).
package task

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
)

var idCounter atomic.Int64

// AgentRunner executes a sub-agent's conversation loop.
// It receives the sub-agent metadata and should block until the agent finishes.
// The runner must update agent.Output and agent.Error before returning.
type AgentRunner func(ctx context.Context, agent *types.SubAgent) error

// Manager handles background task lifecycle.
type Manager struct {
	mu          sync.RWMutex
	tasks       map[string]*types.Task
	agents      map[string]*types.SubAgent
	agentRunner AgentRunner
	warnings    chan string // Channel for timeout warnings
	stopMon     chan struct{}

	// Live output buffers for running tasks. Keyed by task ID.
	// Written to by the bash command's stdout/stderr; read via
	// GetTaskOutput() which copies the buffer under the lock.
	liveOutput map[string]*syncBuffer
}

// NewManager creates a task manager.
func NewManager() *Manager {
	m := &Manager{
		tasks:      make(map[string]*types.Task),
		agents:     make(map[string]*types.SubAgent),
		liveOutput: make(map[string]*syncBuffer),
		warnings:   make(chan string, 100),
		stopMon:    make(chan struct{}),
	}
	go m.monitorTasks()
	return m
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

// runBashTask executes a bash command in the background, streaming output
// into a thread-safe buffer so live output is readable via GetTaskOutput
// while the command is still running (used by the CLI's live progress display).
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

	// Run in a new process group so cancellation/timeout kills the entire
	// process tree, not just the top-level bash.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second

	// Stream output into a thread-safe buffer stored in liveOutput.
	// GetTaskOutput() reads from this buffer while the command runs.
	var buf syncBuffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	m.mu.Lock()
	m.liveOutput[task.ID] = &buf
	m.mu.Unlock()

	err := cmd.Run()
	now := time.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Move final output from buffer to task and remove the live buffer.
	delete(m.liveOutput, task.ID)

	// Already killed/stopped — don't overwrite terminal state.
	if task.Status.IsTerminal() {
		return
	}

	task.EndTime = &now
	task.Output = buf.String()

	if err != nil {
		task.Status = types.TaskStatusFailed

		switch {
		case ctx.Err() == context.DeadlineExceeded:
			task.Error = fmt.Sprintf("command timed out after %d seconds", task.Timeout)
			task.Output += fmt.Sprintf("\n\n⏱️  TIMEOUT: Command exceeded %d second limit.\n", task.Timeout)
			task.Output += "Consider:\n"
			task.Output += "  - Increasing the timeout if the task legitimately needs more time\n"
			task.Output += "  - Breaking the task into smaller steps\n"
			task.Output += "  - Checking if the command is stuck waiting for input\n"
		default:
			task.Error = err.Error()
		}

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

// GetTaskOutput returns the current output for a task, including live
// output from still-running commands. Safe for concurrent use.
func (m *Manager) GetTaskOutput(taskID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// If there's a live buffer (command still running), read from it.
	if buf, ok := m.liveOutput[taskID]; ok {
		return buf.String()
	}

	// Otherwise, return the finalized output from the task.
	if task, ok := m.tasks[taskID]; ok {
		return task.Output
	}
	return ""
}

// TaskSnapshot is a point-in-time copy of a Task's fields, safe to read
// without holding the manager lock.
type TaskSnapshot struct {
	ID          string
	Type        types.TaskType
	Status      types.TaskStatus
	Description string
	StartTime   time.Time
	EndTime     *time.Time
	Output      string
	Error       string
	ExitCode    *int
	Command     string
}

// GetTaskSnapshot returns a race-free copy of task fields plus live output.
func (m *Manager) GetTaskSnapshot(taskID string) (TaskSnapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	task, ok := m.tasks[taskID]
	if !ok {
		return TaskSnapshot{}, false
	}

	snap := TaskSnapshot{
		ID:          task.ID,
		Type:        task.Type,
		Status:      task.Status,
		Description: task.Description,
		StartTime:   task.StartTime,
		EndTime:     task.EndTime,
		Error:       task.Error,
		ExitCode:    task.ExitCode,
		Command:     task.Command,
		Output:      task.Output,
	}

	// If there's a live buffer, prefer its content over task.Output.
	if buf, ok := m.liveOutput[taskID]; ok {
		snap.Output = buf.String()
	}

	return snap, true
}

// AgentSnapshot is a point-in-time copy of a SubAgent's fields.
type AgentSnapshot struct {
	ID          string
	SessionID   string
	Type        string
	Status      types.TaskStatus
	Description string
	StartTime   time.Time
	EndTime     *time.Time
	Output      string
	Error       string
	TurnCount   int
	MaxTurns    int
}

// GetAgentSnapshot returns a race-free copy of sub-agent fields.
func (m *Manager) GetAgentSnapshot(agentID string) (AgentSnapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agent, ok := m.agents[agentID]
	if !ok {
		return AgentSnapshot{}, false
	}

	return AgentSnapshot{
		ID:          agent.ID,
		SessionID:   agent.SessionID,
		Type:        agent.Type,
		Status:      agent.Status,
		Description: agent.Description,
		StartTime:   agent.StartTime,
		EndTime:     agent.EndTime,
		Output:      agent.Output,
		Error:       agent.Error,
		TurnCount:   agent.TurnCount,
		MaxTurns:    agent.MaxTurns,
	}, true
}

// syncBuffer is a goroutine-safe bytes.Buffer for streaming command output.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Read implements io.Reader for completeness.
func (b *syncBuffer) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Read(p)
}

// Ensure syncBuffer satisfies io.Writer.
var _ io.Writer = (*syncBuffer)(nil)

// SetAgentRunner configures the function used to execute sub-agents.
// Must be called before any Agent tool invocations (typically during worker setup).
func (m *Manager) SetAgentRunner(runner AgentRunner) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agentRunner = runner
}

// RunAgent spawns a sub-agent in a background goroutine using the configured AgentRunner.
// Returns immediately; the agent's status/output are updated asynchronously.
func (m *Manager) RunAgent(agentID string) error {
	m.mu.Lock()
	agent, ok := m.agents[agentID]
	runner := m.agentRunner
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("agent not found: %s", agentID)
	}

	if runner == nil {
		return fmt.Errorf("no agent runner configured — sub-agent execution unavailable")
	}

	go func() {
		m.updateAgentStatus(agentID, types.TaskStatusRunning)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Store cancel so StopAgent can interrupt
		m.mu.Lock()
		agent.Cancel = cancel
		m.mu.Unlock()

		err := runner(ctx, agent)

		now := time.Now()
		m.mu.Lock()
		defer m.mu.Unlock()

		// Don't overwrite terminal status (e.g., StopAgent already set "killed")
		if agent.Status.IsTerminal() {
			return
		}

		agent.EndTime = &now
		if err != nil {
			agent.Status = types.TaskStatusFailed
			agent.Error = err.Error()
		} else {
			agent.Status = types.TaskStatusCompleted
		}
	}()

	return nil
}

// updateAgentStatus updates a sub-agent's status.
func (m *Manager) updateAgentStatus(agentID string, status types.TaskStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if agent, ok := m.agents[agentID]; ok {
		agent.Status = status
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

// ListTaskSnapshots returns race-free snapshots of all tasks for a session.
func (m *Manager) ListTaskSnapshots(sessionID string) []TaskSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []TaskSnapshot
	for _, t := range m.tasks {
		if t.SessionID != sessionID {
			continue
		}
		snap := TaskSnapshot{
			ID:          t.ID,
			Type:        t.Type,
			Status:      t.Status,
			Description: t.Description,
			StartTime:   t.StartTime,
			EndTime:     t.EndTime,
			Error:       t.Error,
			ExitCode:    t.ExitCode,
			Command:     t.Command,
			Output:      t.Output,
		}
		if buf, ok := m.liveOutput[t.ID]; ok {
			snap.Output = buf.String()
		}
		out = append(out, snap)
	}
	return out
}

// ListAgentSnapshots returns race-free snapshots of all sub-agents for a session.
func (m *Manager) ListAgentSnapshots(parentSessionID string) []AgentSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []AgentSnapshot
	for _, a := range m.agents {
		if a.ParentSessionID != parentSessionID {
			continue
		}
		out = append(out, AgentSnapshot{
			ID:          a.ID,
			SessionID:   a.SessionID,
			Type:        a.Type,
			Status:      a.Status,
			Description: a.Description,
			StartTime:   a.StartTime,
			EndTime:     a.EndTime,
			Output:      a.Output,
			Error:       a.Error,
			TurnCount:   a.TurnCount,
			MaxTurns:    a.MaxTurns,
		})
	}
	return out
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

	if agent.Cancel != nil {
		agent.Cancel()
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
	return fmt.Sprintf("%s%d", prefix, idCounter.Add(1))
}

// generateSessionID creates a unique session ID for sub-agents.
func generateSessionID() string {
	return fmt.Sprintf("subagent-%d", idCounter.Add(1))
}

// monitorTasks periodically checks for long-running tasks without timeouts.
func (m *Manager) monitorTasks() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.checkLongRunningTasks()
		case <-m.stopMon:
			return
		}
	}
}

// checkLongRunningTasks warns about tasks running suspiciously long.
func (m *Manager) checkLongRunningTasks() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	for _, task := range m.tasks {
		if task.Status.IsTerminal() {
			continue
		}

		duration := now.Sub(task.StartTime)

		// Warn if task has no timeout and has been running > 5 minutes
		if task.Timeout == 0 && duration > 5*time.Minute {
			select {
			case m.warnings <- fmt.Sprintf(
				"⚠️  Task %s (%s) has been running for %s without a timeout. Consider using TaskStop(\"%s\") if it's stuck.",
				task.ID, task.Description, duration.Round(time.Second), task.ID):
			default:
				// Channel full, skip warning
			}
		}

		// Warn if task is approaching its timeout (90% of timeout elapsed)
		if task.Timeout > 0 {
			timeoutDuration := time.Duration(task.Timeout) * time.Second
			if duration > timeoutDuration*9/10 && duration < timeoutDuration {
				select {
				case m.warnings <- fmt.Sprintf(
					"⏱️  Task %s (%s) is approaching timeout (%s remaining)",
					task.ID, task.Description, (timeoutDuration - duration).Round(time.Second)):
				default:
				}
			}
		}
	}
}

// GetWarnings returns a channel that receives timeout warnings.
func (m *Manager) GetWarnings() <-chan string {
	return m.warnings
}

// Stop stops the monitoring goroutine.
func (m *Manager) Stop() {
	close(m.stopMon)
}
