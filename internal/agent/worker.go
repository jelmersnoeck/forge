package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jelmersnoeck/forge/internal/mcp"
	rctx "github.com/jelmersnoeck/forge/internal/runtime/context"
	"github.com/jelmersnoeck/forge/internal/runtime/loop"
	"github.com/jelmersnoeck/forge/internal/runtime/provider"
	"github.com/jelmersnoeck/forge/internal/runtime/session"
	"github.com/jelmersnoeck/forge/internal/runtime/task"
	"github.com/jelmersnoeck/forge/internal/tools"
	"github.com/jelmersnoeck/forge/internal/types"
)

// Worker runs the conversation loop for a single session, pulling messages
// from the Hub and streaming events back through it.
type Worker struct {
	hub         *Hub
	sessionID   string
	cwd         string
	sessionsDir string
}

// NewWorker creates a new Worker.
func NewWorker(hub *Hub, sessionID, cwd, sessionsDir string) *Worker {
	return &Worker{
		hub:         hub,
		sessionID:   sessionID,
		cwd:         cwd,
		sessionsDir: sessionsDir,
	}
}

// Run starts the worker message loop. It blocks until the context is cancelled.
func (w *Worker) Run(ctx context.Context) {
	log.Printf("[agent:%s] worker started, cwd=%s", w.sessionID, w.cwd)

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	prov := provider.NewAnthropic(apiKey)
	registry := tools.NewDefaultRegistry()
	loader := rctx.NewLoader(w.cwd)
	bundle, err := loader.Load([]string{"user", "project", "local"})
	if err != nil {
		log.Printf("[agent:%s] context load error: %v", w.sessionID, err)
		bundle = types.ContextBundle{
			AgentDefinitions: make(map[string]types.AgentDefinition),
		}
	}
	store := session.NewStore(w.sessionsDir)

	// Set up task manager with agent runner for sub-agent execution.
	mgr := task.NewManager()
	tools.SetTaskManager(mgr)
	mgr.SetAgentRunner(w.makeAgentRunner(prov, registry, bundle, store))
	defer mgr.Stop()

	// Connect to MCP servers — tools are stored lazily, not registered with the LLM.
	// The UseMCPTool gateway provides on-demand access (~300 tokens vs ~15K+).
	mcpStore := mcp.NewStore()
	tools.SetMCPStore(mcpStore)
	w.connectMCPServers(ctx, mcpStore)
	defer func() {
		for _, c := range mcpStore.Clients() {
			_ = c.Close(context.Background())
		}
	}()

	// Pick model: settings override if it's a real API model ID
	const defaultModel = "claude-opus-4-6"
	model := defaultModel
	if m := bundle.Settings.Model; m != "" && strings.HasPrefix(m, "claude-") {
		model = m
	}

	var historyID string
	for {
		select {
		case <-ctx.Done():
			log.Printf("[agent:%s] worker stopped", w.sessionID)
			return
		default:
		}

		msg := w.hub.PullMessage()
		if len(msg.Text) > 100 {
			log.Printf("[agent:%s] <- %s...", w.sessionID, msg.Text[:100])
		} else {
			log.Printf("[agent:%s] <- %s", w.sessionID, msg.Text)
		}

		opts := loop.Options{
			Provider:     prov,
			Tools:        registry,
			Context:      bundle,
			CWD:          w.cwd,
			SessionStore: store,
			SessionID:    w.sessionID,
			Model:        model,
			MaxTurns:     0, // unlimited
			AuditLogger:  &StdAuditLogger{},
			OnComplete:   autoReflect(w.cwd),
		}
		l := loop.New(opts)

		var emit func(types.OutboundEvent)
		emit = func(event types.OutboundEvent) {
			if event.ID == "" {
				event.ID = uuid.New().String()
			}
			if event.SessionID == "" {
				event.SessionID = w.sessionID
			}
			if event.Timestamp == 0 {
				event.Timestamp = time.Now().UnixMilli()
			}

			// Intercept queue management events
			switch event.Type {
			case "queue_immediate":
				w.hub.EnqueueImmediate(event.Content)
			case "queue_on_complete":
				w.hub.EnqueueCompletion(event.Content)
			case "tool_use":
				// After any tool use, execute immediate queue
				w.executeImmediateQueue(ctx, registry, historyID, emit)
			case "done":
				// Before done event, execute completion queue
				w.executeCompletionQueue(ctx, registry, historyID, emit)
			}

			w.hub.PublishEvent(event)
		}

		var runErr error
		// Create a cancellable context for this turn so interrupts
		// can abort the loop without killing the entire worker.
		turnCtx, turnCancel := context.WithCancel(ctx)
		go func() {
			select {
			case <-w.hub.InterruptChannel():
				turnCancel()
			case <-turnCtx.Done():
			}
		}()

		switch {
		case historyID != "":
			runErr = l.Resume(turnCtx, historyID, msg.Text, emit)
		default:
			runErr = l.Send(turnCtx, msg.Text, emit)
		}
		turnCancel() // clean up goroutine

		if runErr != nil {
			log.Printf("[agent:%s] error: %v", w.sessionID, runErr)

			// Distinguish interrupts from real errors.
			switch turnCtx.Err() {
			case context.Canceled:
				emit(types.OutboundEvent{Type: "error", Content: "Interrupted by user"})
			default:
				emit(types.OutboundEvent{Type: "error", Content: runErr.Error()})
			}

			// Always emit done so the CLI returns to the prompt.
			emit(types.OutboundEvent{Type: "done"})
		}

		historyID = l.HistoryID()
	}
}

// executeImmediateQueue runs all commands in the immediate queue
func (w *Worker) executeImmediateQueue(ctx context.Context, registry *tools.Registry, historyID string, emit func(types.OutboundEvent)) {
	commands := w.hub.GetImmediateQueue()
	if len(commands) == 0 {
		return
	}

	for _, command := range commands {
		log.Printf("[agent:%s] executing immediate queue: %s", w.sessionID, command)
		w.executeQueuedCommand(ctx, registry, historyID, command, "immediate", emit)
	}
}

// executeCompletionQueue runs all commands in the completion queue and clears it
func (w *Worker) executeCompletionQueue(ctx context.Context, registry *tools.Registry, historyID string, emit func(types.OutboundEvent)) {
	commands := w.hub.PullCompletionQueue()
	if len(commands) == 0 {
		return
	}

	for _, command := range commands {
		log.Printf("[agent:%s] executing completion queue: %s", w.sessionID, command)
		w.executeQueuedCommand(ctx, registry, historyID, command, "completion", emit)
	}
}

// executeQueuedCommand executes a single queued bash command
func (w *Worker) executeQueuedCommand(ctx context.Context, registry *tools.Registry, historyID, command, queueType string, emit func(types.OutboundEvent)) {
	toolCtx := types.ToolContext{
		Ctx:       ctx,
		CWD:       w.cwd,
		SessionID: w.sessionID,
		HistoryID: historyID,
		Emit:      emit,
	}

	input := map[string]any{"command": command}
	result, err := registry.Execute("Bash", input, toolCtx)

	if err != nil {
		log.Printf("[agent:%s] queued command failed (%s): %v", w.sessionID, queueType, err)
		emit(types.OutboundEvent{
			Type:     "queued_task_error",
			Content:  fmt.Sprintf("[%s queue] Command failed: %s\nError: %v", queueType, command, err),
			ToolName: "Bash",
		})
		return
	}

	// Emit result
	if len(result.Content) > 0 && result.Content[0].Text != "" {
		emit(types.OutboundEvent{
			Type:     "queued_task_result",
			Content:  fmt.Sprintf("[%s queue] %s\n%s", queueType, command, result.Content[0].Text),
			ToolName: "Bash",
		})
	}
}

// makeAgentRunner returns an AgentRunner that spawns a conversation loop for sub-agents.
//
//	Parent Worker
//	    │
//	    ├─ creates task.Manager with AgentRunner
//	    │
//	    └─ Agent tool invoked by LLM
//	         │
//	         └─ RunAgent(id)
//	              │
//	              └─ goroutine: AgentRunner(ctx, subAgent)
//	                   │
//	                   ├─ Filtered tool registry (allow/deny lists)
//	                   ├─ Sub-agent prompt as system context
//	                   ├─ loop.New(opts).Send(ctx, prompt, emit)
//	                   └─ Captures text output → agent.Output
func (w *Worker) makeAgentRunner(
	prov types.LLMProvider,
	parentRegistry *tools.Registry,
	bundle types.ContextBundle,
	store *session.Store,
) task.AgentRunner {
	return func(ctx context.Context, agent *types.SubAgent) error {
		subRegistry := parentRegistry.Filtered(agent.Tools, agent.DisallowedTools)

		model := agent.Model
		if model == "" {
			const defaultModel = "claude-opus-4-6"
			model = defaultModel
			if m := bundle.Settings.Model; m != "" && strings.HasPrefix(m, "claude-") {
				model = m
			}
		}

		maxTurns := agent.MaxTurns
		if maxTurns < 0 {
			maxTurns = 500 // default for sub-agents when caller didn't specify
		}
		// 0 = unlimited, positive = explicit limit

		opts := loop.Options{
			Provider:     prov,
			Tools:        subRegistry,
			Context:      bundle,
			CWD:          w.cwd,
			SessionStore: store,
			SessionID:    agent.SessionID,
			Model:        model,
			MaxTurns:     maxTurns,
			AuditLogger:  &StdAuditLogger{},
		}

		l := loop.New(opts)

		// Collect text output from the sub-agent.
		var output strings.Builder
		emit := func(event types.OutboundEvent) {
			switch event.Type {
			case "text":
				output.WriteString(event.Content)
			}
		}

		if err := l.Send(ctx, agent.Prompt, emit); err != nil {
			agent.Output = output.String()
			return err
		}

		agent.Output = output.String()
		return nil
	}
}

// connectMCPServers loads MCP config and connects to all configured servers,
// storing their tool catalogs in the MCP store for lazy access.
func (w *Worker) connectMCPServers(ctx context.Context, store *mcp.Store) {
	cfg, err := mcp.LoadConfig(w.cwd)
	if err != nil {
		log.Printf("[agent:%s] MCP config load error (continuing without MCP): %v", w.sessionID, err)
		return
	}

	if len(cfg.Servers) == 0 {
		return
	}

	var tokenStore *mcp.TokenStore

	for name, serverCfg := range cfg.Servers {
		// Lazy-init token store only if an OAuth server exists
		if serverCfg.Auth == "oauth" && tokenStore == nil {
			tokenStore, err = mcp.NewTokenStore()
			if err != nil {
				log.Printf("[agent:%s] failed to create MCP token store: %v", w.sessionID, err)
				continue
			}
		}

		_, err := mcp.ConnectAndStore(ctx, store, name, serverCfg, tokenStore)
		if err != nil {
			log.Printf("[agent:%s] MCP server %q connect failed (skipping): %v", w.sessionID, name, err)
			continue
		}

		log.Printf("[agent:%s] MCP server %q connected (lazy tools)", w.sessionID, name)
	}
}

// autoReflect returns an OnComplete callback that saves a reflection summary
// built from conversation history. Errors are logged, never propagated.
func autoReflect(cwd string) func([]types.ChatMessage) {
	return func(history []types.ChatMessage) {
		summary := buildReflectionSummary(history)
		if summary == "" {
			return
		}
		if err := tools.SaveReflection(cwd, summary); err != nil {
			log.Printf("[auto-reflect] failed to save reflection: %v", err)
		}
	}
}

// buildReflectionSummary extracts a short summary from conversation history.
//
// Format: "User asked: <first prompt>. Tools used: Bash, Edit, Read (5 calls)"
func buildReflectionSummary(history []types.ChatMessage) string {
	var userPrompt string
	toolCounts := map[string]int{}

	for _, msg := range history {
		for _, block := range msg.Content {
			switch {
			case block.Type == "text" && msg.Role == "user" && userPrompt == "":
				userPrompt = block.Text
			case block.Type == "tool_use":
				toolCounts[block.Name]++
			}
		}
	}

	if len(toolCounts) == 0 {
		return ""
	}

	// Truncate prompt to keep summary concise.
	const maxPromptLen = 120
	prompt := userPrompt
	if len(prompt) > maxPromptLen {
		prompt = prompt[:maxPromptLen] + "..."
	}

	var toolNames []string
	total := 0
	for name, count := range toolCounts {
		toolNames = append(toolNames, name)
		total += count
	}
	sort.Strings(toolNames)

	return fmt.Sprintf("User asked: %s. Tools used: %s (%d calls)", prompt, strings.Join(toolNames, ", "), total)
}
