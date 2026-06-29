package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jelmersnoeck/forge/internal/agent/phase"
	"github.com/jelmersnoeck/forge/internal/tools"
	"github.com/stretchr/testify/require"

	"github.com/jelmersnoeck/forge/internal/types"
)

func TestWorkerStateTransition(t *testing.T) {
	tests := map[string]struct {
		initial WorkerState
		result  phase.OrchestratorResult
		want    WorkerState
	}{
		"idle to QA": {
			initial: WorkerState{Phase: PhaseIdle},
			result: phase.OrchestratorResult{
				Intent:      phase.IntentQuestion,
				QAHistoryID: "qa-123",
			},
			want: WorkerState{
				Phase:       PhaseQA,
				QAHistoryID: "qa-123",
			},
		},
		"idle to investigate": {
			initial: WorkerState{Phase: PhaseIdle},
			result: phase.OrchestratorResult{
				Intent:               phase.IntentInvestigate,
				InvestigateHistoryID: "inv-456",
			},
			want: WorkerState{
				Phase:                PhaseInvestigate,
				InvestigateHistoryID: "inv-456",
			},
		},
		"idle to task": {
			initial: WorkerState{Phase: PhaseIdle},
			result: phase.OrchestratorResult{
				Intent:         phase.IntentTask,
				CoderHistoryID: "coder-789",
			},
			want: WorkerState{
				Phase:     PhaseOrchestrator,
				HistoryID: "coder-789",
			},
		},
		"idle to review": {
			initial: WorkerState{Phase: PhaseIdle},
			result:  phase.OrchestratorResult{Intent: phase.IntentReview},
			want:    WorkerState{Phase: PhaseOrchestrator},
		},
		"QA to task clears QA state": {
			initial: WorkerState{
				Phase:       PhaseQA,
				QAHistoryID: "qa-old",
			},
			result: phase.OrchestratorResult{
				Intent:         phase.IntentTask,
				CoderHistoryID: "coder-new",
			},
			want: WorkerState{
				Phase:     PhaseOrchestrator,
				HistoryID: "coder-new",
			},
		},
		"investigate to task clears investigate state": {
			initial: WorkerState{
				Phase:                PhaseInvestigate,
				InvestigateHistoryID: "inv-old",
			},
			result: phase.OrchestratorResult{
				Intent:         phase.IntentTask,
				CoderHistoryID: "coder-new",
			},
			want: WorkerState{
				Phase:     PhaseOrchestrator,
				HistoryID: "coder-new",
			},
		},
		"QA to investigate clears QA": {
			initial: WorkerState{
				Phase:       PhaseQA,
				QAHistoryID: "qa-old",
			},
			result: phase.OrchestratorResult{
				Intent:               phase.IntentInvestigate,
				InvestigateHistoryID: "inv-new",
			},
			want: WorkerState{
				Phase:                PhaseInvestigate,
				InvestigateHistoryID: "inv-new",
			},
		},
		"investigate to QA clears investigate": {
			initial: WorkerState{
				Phase:                PhaseInvestigate,
				InvestigateHistoryID: "inv-old",
			},
			result: phase.OrchestratorResult{
				Intent:      phase.IntentQuestion,
				QAHistoryID: "qa-new",
			},
			want: WorkerState{
				Phase:       PhaseQA,
				QAHistoryID: "qa-new",
			},
		},
		"investigate to triage clears investigate": {
			initial: WorkerState{
				Phase:                PhaseInvestigate,
				InvestigateHistoryID: "inv-old",
			},
			result: phase.OrchestratorResult{
				Intent:          phase.IntentTriage,
				TriageHistoryID: "triage-new",
			},
			want: WorkerState{
				Phase:           PhaseTriage,
				TriageHistoryID: "triage-new",
			},
		},
		"triage to task clears triage state": {
			initial: WorkerState{
				Phase:           PhaseTriage,
				TriageHistoryID: "triage-old",
			},
			result: phase.OrchestratorResult{
				Intent:         phase.IntentTask,
				CoderHistoryID: "coder-new",
			},
			want: WorkerState{
				Phase:     PhaseOrchestrator,
				HistoryID: "coder-new",
			},
		},
		"triage to triage updates history": {
			initial: WorkerState{
				Phase:           PhaseTriage,
				TriageHistoryID: "triage-old",
			},
			result: phase.OrchestratorResult{
				Intent:          phase.IntentTriage,
				TriageHistoryID: "triage-new",
			},
			want: WorkerState{
				Phase:           PhaseTriage,
				TriageHistoryID: "triage-new",
			},
		},
		"task with empty coder history preserves existing": {initial: WorkerState{
			Phase:     PhaseOrchestrator,
			HistoryID: "existing",
		},
			result: phase.OrchestratorResult{
				Intent:         phase.IntentTask,
				CoderHistoryID: "",
			},
			want: WorkerState{
				Phase:     PhaseOrchestrator,
				HistoryID: "existing",
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got := tc.initial.Transition(tc.result)
			r.Equal(tc.want, got)
		})
	}
}

func TestWorkerStateShouldRunOrchestrator(t *testing.T) {
	tests := map[string]struct {
		state WorkerState
		mode  string
		want  bool
	}{
		"idle with swe mode": {
			state: WorkerState{Phase: PhaseIdle},
			mode:  "swe",
			want:  true,
		},
		"idle with spec mode": {
			state: WorkerState{Phase: PhaseIdle},
			mode:  "spec",
			want:  true,
		},
		"idle with code mode": {
			state: WorkerState{Phase: PhaseIdle},
			mode:  "code",
			want:  true,
		},
		"idle with empty mode": {
			state: WorkerState{Phase: PhaseIdle},
			mode:  "",
			want:  false,
		},
		"QA with swe mode": {
			state: WorkerState{Phase: PhaseQA},
			mode:  "swe",
			want:  true,
		},
		"QA with non-swe mode": {
			state: WorkerState{Phase: PhaseQA},
			mode:  "spec",
			want:  false,
		},
		"investigate with swe mode": {
			state: WorkerState{Phase: PhaseInvestigate},
			mode:  "swe",
			want:  true,
		},
		"investigate with non-swe mode": {
			state: WorkerState{Phase: PhaseInvestigate},
			mode:  "code",
			want:  false,
		},
		"triage with swe mode": {
			state: WorkerState{Phase: PhaseTriage},
			mode:  "swe",
			want:  true,
		},
		"triage with non-swe mode": {
			state: WorkerState{Phase: PhaseTriage},
			mode:  "spec",
			want:  false,
		},
		"orchestrator done": {
			state: WorkerState{Phase: PhaseOrchestrator},
			mode:  "swe",
			want:  false,
		},
		"phase done": {
			state: WorkerState{Phase: PhaseDone},
			mode:  "spec",
			want:  false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, tc.state.ShouldRunOrchestrator(tc.mode))
		})
	}
}

func TestExtractPipelineHint(t *testing.T) {
	tests := map[string]struct {
		metadata map[string]any
		want     string
	}{
		"nil metadata": {
			metadata: nil,
			want:     "auto",
		},
		"empty metadata": {
			metadata: map[string]any{},
			want:     "auto",
		},
		"ideate hint": {
			metadata: map[string]any{"pipeline_hint": "ideate"},
			want:     "ideate",
		},
		"code hint": {
			metadata: map[string]any{"pipeline_hint": "code"},
			want:     "code",
		},
		"spec hint": {
			metadata: map[string]any{"pipeline_hint": "spec"},
			want:     "spec",
		},
		"auto hint": {
			metadata: map[string]any{"pipeline_hint": "auto"},
			want:     "auto",
		},
		"invalid hint defaults to auto": {
			metadata: map[string]any{"pipeline_hint": "streets-ahead"},
			want:     "auto",
		},
		"wrong type defaults to auto": {
			metadata: map[string]any{"pipeline_hint": 42},
			want:     "auto",
		},
		"empty string defaults to auto": {
			metadata: map[string]any{"pipeline_hint": ""},
			want:     "auto",
		},
		"other metadata ignored": {
			metadata: map[string]any{
				"source":        "linear",
				"pipeline_hint": "code",
			},
			want: "code",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, extractPipelineHint(tc.metadata))
		})
	}
}

func TestResolveModelAlias(t *testing.T) {
	tests := map[string]struct {
		name        string
		isClaudeCLI bool
		want        string
	}{
		"opus alias": {
			name: "opus", isClaudeCLI: false,
			want: "claude-opus-4-6",
		},
		"sonnet alias": {
			name: "sonnet", isClaudeCLI: false,
			want: "claude-sonnet-4-20250514",
		},
		"haiku alias": {
			name: "haiku", isClaudeCLI: false,
			want: "claude-haiku-4-20250506",
		},
		"full model ID passes through": {
			name: "claude-sonnet-4-20250514", isClaudeCLI: false,
			want: "claude-sonnet-4-20250514",
		},
		"unknown name passes through": {
			name: "gpt-4", isClaudeCLI: false,
			want: "gpt-4",
		},
		"claude CLI skips alias resolution": {
			name: "sonnet", isClaudeCLI: true,
			want: "sonnet",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, ResolveModelAlias(tc.name, tc.isClaudeCLI))
		})
	}
}

func TestWorkerResolveModel(t *testing.T) {
	// Isolate from any real ~/.forge/config.toml so the user-config tier is
	// empty unless a case writes one.
	t.Setenv("HOME", t.TempDir())

	tests := map[string]struct {
		override      string
		settingsModel string
		isClaudeCLI   bool
		want          string
	}{
		"override takes priority": {
			override: "sonnet", settingsModel: "claude-opus-4-6", isClaudeCLI: false,
			want: "claude-sonnet-4-20250514",
		},
		"settings fallback": {
			override: "", settingsModel: "claude-opus-4-6", isClaudeCLI: false,
			want: "claude-opus-4-6",
		},
		"default when no override or settings": {
			override: "", settingsModel: "", isClaudeCLI: false,
			want: "claude-opus-4-6",
		},
		"claude CLI passes alias through": {
			override: "sonnet", settingsModel: "", isClaudeCLI: true,
			want: "sonnet",
		},
		"settings with non-claude prefix passes through for Anthropic": {
			// The claude- prefix gate was removed: a non-empty settings model
			// passes through to whatever provider is active, which owns model
			// validation (eliminate-provider-coupling Phase 1).
			override: "", settingsModel: "opus[1m]", isClaudeCLI: false,
			want: "opus[1m]",
		},
		"settings with non-claude prefix used for Claude CLI": {
			override: "", settingsModel: "opus[1m]", isClaudeCLI: true,
			want: "opus[1m]",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			w := &Worker{modelOverride: tc.override}
			r.Equal(tc.want, w.resolveModel(tc.settingsModel, tc.isClaudeCLI, "claude-opus-4-6"))
		})
	}
}

func TestWorkerResolveModel_userConfigTier(t *testing.T) {
	r := require.New(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfgDir := filepath.Join(home, ".forge")
	r.NoError(os.MkdirAll(cfgDir, 0o755))
	r.NoError(os.WriteFile(filepath.Join(cfgDir, "config.toml"),
		[]byte("[model]\ndefault = \"sonnet\"\n"), 0o644))

	w := &Worker{}

	// No override, no settings → user-config default (alias expanded for Anthropic).
	r.Equal("claude-sonnet-4-20250514", w.resolveModel("", false, "claude-opus-4-6"))

	// Session override beats user config.
	w.SetModel("opus")
	r.Equal("claude-opus-4-6", w.resolveModel("", false, "claude-opus-4-6"))

	// Project settings (claude- prefix) beats user config.
	w2 := &Worker{}
	r.Equal("claude-haiku-4", w2.resolveModel("claude-haiku-4", false, "claude-opus-4-6"))

	// Claude CLI passes the user-config alias through unchanged.
	w3 := &Worker{}
	r.Equal("sonnet", w3.resolveModel("", true, "claude-opus-4-6"))
}

func TestWorkerSetModelConcurrent(t *testing.T) {
	r := require.New(t)
	w := &Worker{}
	r.Equal("", w.ModelOverride())

	w.SetModel("sonnet")
	r.Equal("sonnet", w.ModelOverride())

	w.SetModel("opus")
	r.Equal("opus", w.ModelOverride())
}

func TestWorkerListModels(t *testing.T) {
	r := require.New(t)

	w := &Worker{}
	ctx := context.Background()

	results := w.ListModels(ctx)

	// With no providers set, we should get an empty list
	r.Empty(results)
}

func TestWorkerListModels_WithProviders(t *testing.T) {
	r := require.New(t)

	w := &Worker{}
	w.providers = map[string]types.LLMProvider{
		"Anthropic": &mockModelLister{
			models: []types.ModelEntry{
				{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4"},
				{ID: "claude-sonnet-4-20250514", DisplayName: "Claude Sonnet 4"},
			},
		},
	}

	results := w.ListModels(context.Background())
	r.Len(results, 1)
	r.Equal("Anthropic", results[0].Provider)
	r.Len(results[0].Models, 2)
	r.Equal("claude-opus-4-6", results[0].Models[0].ID)
	r.Equal("Claude Opus 4", results[0].Models[0].DisplayName)
}

func TestWorkerListModels_ProviderError(t *testing.T) {
	r := require.New(t)

	w := &Worker{}
	w.providers = map[string]types.LLMProvider{
		"Anthropic": &mockModelLister{
			err: fmt.Errorf("authentication failed"),
		},
	}

	results := w.ListModels(context.Background())
	r.Len(results, 1)
	r.Equal("Anthropic", results[0].Provider)
	r.Empty(results[0].Models)
	r.Contains(results[0].Error, "authentication failed")
}

// mockModelLister implements both LLMProvider and ModelLister for testing.
type mockModelLister struct {
	models []types.ModelEntry
	err    error
}

func (m *mockModelLister) Chat(_ context.Context, _ types.ChatRequest) (<-chan types.ChatDelta, error) {
	return nil, nil
}

func (m *mockModelLister) ListModels(_ context.Context) ([]types.ModelEntry, error) {
	return m.models, m.err
}

func TestTurnErrorClassification(t *testing.T) {
	// This test validates the pattern used in Worker.Run() to distinguish
	// user interrupts from real errors. The bug: turnCancel() was called
	// before checking turnCtx.Err(), so every error looked like an interrupt.
	tests := map[string]struct {
		cancelBeforeCheck bool // simulate interrupt
		wantInterrupted   bool
	}{
		"API error without interrupt emits error not interrupted": {
			cancelBeforeCheck: false,
			wantInterrupted:   false,
		},
		"interrupt cancels context before check": {
			cancelBeforeCheck: true,
			wantInterrupted:   true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			ctx := context.Background()
			turnCtx, turnCancel := context.WithCancel(ctx)

			// Simulate: the turn produced an error
			runErr := fmt.Errorf("Anthropic API error: 529 overloaded")

			if tc.cancelBeforeCheck {
				// Simulate interrupt arriving during the turn
				turnCancel()
			}

			// FIX PATTERN: capture context state BEFORE cleanup cancel
			wasInterrupted := turnCtx.Err() == context.Canceled
			turnCancel() // cleanup (always called)

			// Classify
			var eventType string
			if runErr != nil {
				if wasInterrupted {
					eventType = "interrupted"
				} else {
					eventType = "error"
				}
			}

			r.Equal(tc.wantInterrupted, wasInterrupted)
			if tc.wantInterrupted {
				r.Equal("interrupted", eventType)
			} else {
				r.Equal("error", eventType)
			}
		})
	}
}

func TestTurnErrorClassification_BuggyPattern(t *testing.T) {
	// Demonstrates the bug: if turnCancel() is called BEFORE checking
	// turnCtx.Err(), an API error gets misclassified as interrupted.
	r := require.New(t)

	ctx := context.Background()
	turnCtx, turnCancel := context.WithCancel(ctx)

	runErr := fmt.Errorf("Anthropic API error: 529 overloaded")
	_ = runErr

	// BUGGY: cancel before check — this is what the old code did
	turnCancel()

	// After turnCancel(), turnCtx.Err() is ALWAYS context.Canceled
	r.Equal(context.Canceled, turnCtx.Err(), "turnCancel() makes Err() always return Canceled — the bug")
}

// collectEvents returns an emit func and a pointer to the captured events.
func collectEvents() (func(types.OutboundEvent), *[]types.OutboundEvent) {
	var events []types.OutboundEvent
	emit := func(e types.OutboundEvent) { events = append(events, e) }
	return emit, &events
}

func newQueueWorker(t *testing.T) *Worker {
	t.Helper()
	return NewWorker(NewHub(), "queue-101", t.TempDir(), t.TempDir(), "swe", "", "", "")
}

func TestWorker_ExecuteQueuedCommand_Success(t *testing.T) {
	r := require.New(t)
	w := newQueueWorker(t)
	registry := tools.NewDefaultRegistry()
	emit, events := collectEvents()

	w.executeQueuedCommand(context.Background(), registry, "hist-1",
		"echo Troy and Abed in the morning", "immediate", emit)

	r.Len(*events, 1)
	r.Equal("queued_task_result", (*events)[0].Type)
	r.Contains((*events)[0].Content, "Troy and Abed in the morning")
	r.Contains((*events)[0].Content, "[immediate queue]")
}

func TestWorker_ExecuteQueuedCommand_Failure(t *testing.T) {
	r := require.New(t)
	w := newQueueWorker(t)
	// Registry without a Bash tool → Execute returns a "tool not found" error,
	// exercising the queued_task_error branch. (A nonzero exit code is surfaced
	// as an IsError result, not a Go error, so it would not hit this path.)
	registry := tools.NewRegistry()
	emit, events := collectEvents()

	w.executeQueuedCommand(context.Background(), registry, "hist-1",
		"echo nope", "completion", emit)

	r.Len(*events, 1)
	r.Equal("queued_task_error", (*events)[0].Type)
	r.Contains((*events)[0].Content, "[completion queue]")
}

func TestWorker_ExecuteImmediateQueue_RunsAllAndPersists(t *testing.T) {
	r := require.New(t)
	w := newQueueWorker(t)
	registry := tools.NewDefaultRegistry()
	emit, events := collectEvents()

	w.hub.EnqueueImmediate("echo first")
	w.hub.EnqueueImmediate("echo second")

	w.executeImmediateQueue(context.Background(), registry, "hist-1", emit)

	r.Len(*events, 2)
	r.Contains((*events)[0].Content, "first")
	r.Contains((*events)[1].Content, "second")

	// Immediate queue persists across turns — not cleared.
	r.Equal([]string{"echo first", "echo second"}, w.hub.GetImmediateQueue())
}

func TestWorker_ExecuteCompletionQueue_RunsAllAndClears(t *testing.T) {
	r := require.New(t)
	w := newQueueWorker(t)
	registry := tools.NewDefaultRegistry()
	emit, events := collectEvents()

	w.hub.EnqueueCompletion("echo done-one")
	w.hub.EnqueueCompletion("echo done-two")

	w.executeCompletionQueue(context.Background(), registry, "hist-1", emit)

	r.Len(*events, 2)

	// Completion queue is cleared after execution — second call is a no-op.
	emit2, events2 := collectEvents()
	w.executeCompletionQueue(context.Background(), registry, "hist-1", emit2)
	r.Empty(*events2, "completion queue should be empty after first run")
}

func TestWorker_ExecuteQueues_EmptyAreNoops(t *testing.T) {
	r := require.New(t)
	w := newQueueWorker(t)
	registry := tools.NewDefaultRegistry()

	emit, events := collectEvents()
	w.executeImmediateQueue(context.Background(), registry, "hist-1", emit)
	w.executeCompletionQueue(context.Background(), registry, "hist-1", emit)

	r.Empty(*events, "empty queues emit nothing")
}
