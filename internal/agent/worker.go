package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	rctx "github.com/jelmersnoeck/forge/internal/runtime/context"
	"github.com/jelmersnoeck/forge/internal/runtime/loop"
	"github.com/jelmersnoeck/forge/internal/runtime/provider"
	"github.com/jelmersnoeck/forge/internal/runtime/session"
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

	// Pick model: settings override if it's a real API model ID
	const defaultModel = "claude-sonnet-4-5-20250929"
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
			MaxTurns:     100,
			AuditLogger:  &StdAuditLogger{},
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
		switch {
		case historyID != "":
			runErr = l.Resume(ctx, historyID, msg.Text, emit)
		default:
			runErr = l.Send(ctx, msg.Text, emit)
		}

		if runErr != nil {
			log.Printf("[agent:%s] error: %v", w.sessionID, runErr)
			emit(types.OutboundEvent{Type: "error", Content: runErr.Error()})
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
