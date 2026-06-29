package phase

import (
	"context"
	"testing"

	"github.com/jelmersnoeck/forge/internal/runtime/session"
	"github.com/jelmersnoeck/forge/internal/tools"
	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSpecCreator_ResumesFromQAHistory(t *testing.T) {
	// When transitioning from Q&A to task, the spec-creator should resume
	// from the Q&A conversation history so it has full context.
	r := require.New(t)
	prov := &trackingProvider{}
	opts := makeTestOrchestratorOpts(t, prov)
	opts.InitialPrompt = "What's the air conditioning annex layout?"
	orch := NewSWEOrchestrator()

	// Simulate a Q&A conversation that produced a history.
	qaHistoryID, err := orch.runQA(context.Background(), opts)
	r.NoError(err)
	r.NotEmpty(qaHistoryID)

	// Now transition: spec-creator should resume from the Q&A history.
	opts.InitialPrompt = "Ok build the annex security system"
	opts.TransitionHistoryID = qaHistoryID

	specResult, err := orch.runSpecCreator(context.Background(), opts)
	r.NoError(err)
	r.NotEmpty(specResult.HistoryID)

	// The spec-creator call should have loaded the Q&A history (more messages).
	prov.mu.Lock()
	counts := make([]int, len(prov.msgCounts))
	copy(counts, prov.msgCounts)
	prov.mu.Unlock()

	r.Equal(2, len(counts), "expected 2 Chat calls (Q&A + spec-creator)")
	r.Greater(counts[1], counts[0],
		"Spec-creator resumed from Q&A should have more messages than the Q&A start")
}

func TestSpecCreator_ResumesFromInvestigateHistory(t *testing.T) {
	r := require.New(t)
	prov := &trackingProvider{}
	opts := makeTestOrchestratorOpts(t, prov)
	opts.InitialPrompt = "Investigate the Greendale Human Being mascot code"
	orch := NewSWEOrchestrator()

	// Simulate an investigation that produced a history.
	investigateHistoryID, err := orch.runInvestigate(context.Background(), opts)
	r.NoError(err)
	r.NotEmpty(investigateHistoryID)

	// Transition: spec-creator should resume from investigation history.
	opts.InitialPrompt = "Now fix the mascot rendering bug"
	opts.TransitionHistoryID = investigateHistoryID

	specResult, err := orch.runSpecCreator(context.Background(), opts)
	r.NoError(err)
	r.NotEmpty(specResult.HistoryID)

	prov.mu.Lock()
	counts := make([]int, len(prov.msgCounts))
	copy(counts, prov.msgCounts)
	prov.mu.Unlock()

	r.Equal(2, len(counts))
	r.Greater(counts[1], counts[0],
		"Spec-creator resumed from investigation should have more messages")
}

func TestCoderDirect_ResumesFromQAHistory(t *testing.T) {
	// Small task after Q&A: the direct coder should also resume from Q&A history.
	r := require.New(t)
	prov := &trackingProvider{}
	opts := makeTestOrchestratorOpts(t, prov)
	opts.InitialPrompt = "How does the paintball scoring work?"
	orch := NewSWEOrchestrator()

	qaHistoryID, err := orch.runQA(context.Background(), opts)
	r.NoError(err)
	r.NotEmpty(qaHistoryID)

	opts.InitialPrompt = "Fix the typo in the score display"
	opts.TransitionHistoryID = qaHistoryID

	coderHistoryID, err := orch.runCoderDirect(context.Background(), opts)
	r.NoError(err)
	r.NotEmpty(coderHistoryID)

	prov.mu.Lock()
	counts := make([]int, len(prov.msgCounts))
	copy(counts, prov.msgCounts)
	prov.mu.Unlock()

	r.Equal(2, len(counts))
	r.Greater(counts[1], counts[0],
		"Direct coder resumed from Q&A should have more messages")
}

func TestSpecCreator_FreshWhenNoTransitionHistory(t *testing.T) {
	// No prior history — spec-creator starts fresh (current behavior unchanged).
	r := require.New(t)
	prov := &trackingProvider{}
	opts := makeTestOrchestratorOpts(t, prov)
	opts.InitialPrompt = "Build a blanket fort simulator"
	orch := NewSWEOrchestrator()

	specResult, err := orch.runSpecCreator(context.Background(), opts)
	r.NoError(err)
	r.NotEmpty(specResult.HistoryID)

	prov.mu.Lock()
	counts := make([]int, len(prov.msgCounts))
	copy(counts, prov.msgCounts)
	prov.mu.Unlock()

	r.Equal(1, len(counts))
	r.Equal(1, counts[0], "Fresh spec-creator should have exactly 1 user message")
}

func TestSpecCreator_FallbackOnMissingHistory(t *testing.T) {
	// If the history file for the transition is missing/corrupt,
	// Resume() should fail and we fall back to Send().
	r := require.New(t)
	prov := &trackingProvider{}
	opts := makeTestOrchestratorOpts(t, prov)
	opts.InitialPrompt = "Build the Dreamatorium"
	opts.TransitionHistoryID = "nonexistent-history-id-from-troy-and-abed"
	orch := NewSWEOrchestrator()

	// Should not error — falls back to Send().
	specResult, err := orch.runSpecCreator(context.Background(), opts)
	r.NoError(err)
	r.NotEmpty(specResult.HistoryID)

	prov.mu.Lock()
	counts := make([]int, len(prov.msgCounts))
	copy(counts, prov.msgCounts)
	prov.mu.Unlock()

	r.Equal(1, len(counts), "Fallback should result in exactly 1 Chat call")
	r.Equal(1, counts[0], "Fallback should send a fresh message (1 user msg)")
}

func TestOrchestrator_QAToTaskTransitionCarriesHistory(t *testing.T) {
	// End-to-end: Q&A response, then task classification on follow-up.
	// The orchestrator should set TransitionHistoryID from the Q&A history.
	r := require.New(t)

	// Provider returns "question" first, then "task/small" on second call.
	callCount := 0
	prov := &mockProvider{
		responses: map[string][]types.ChatDelta{},
	}
	// Lightweight models return question first, then task/small.
	for _, m := range testLightweightModels {
		prov.responses[m] = []types.ChatDelta{
			{Type: "text_delta", Text: `{"intent":"question","size":"","spec_match":""}`},
		}
	}
	prov.responses["test-model"] = []types.ChatDelta{
		{Type: "text_delta", Text: "The study room is on the second floor."},
		{Type: "usage", Usage: &types.TokenUsage{InputTokens: 100, OutputTokens: 50}},
		{Type: "message_stop", StopReason: "end_turn"},
	}

	opts := makeTestOrchestratorOpts(t, prov)
	opts.InitialPrompt = "Where is study room F?"
	orch := NewSWEOrchestrator()

	// First run: should classify as question, return QA history.
	result, err := orch.Run(context.Background(), opts)
	r.NoError(err)
	r.Equal(IntentQuestion, result.Intent)
	r.NotEmpty(result.QAHistoryID)
	qaHistoryID := result.QAHistoryID
	_ = callCount

	// Now switch classifier to return task/small for the follow-up.
	for _, m := range testLightweightModels {
		prov.responses[m] = []types.ChatDelta{
			{Type: "text_delta", Text: `{"intent":"task","size":"small","spec_match":""}`},
		}
	}

	// Second run: Q&A → task transition.
	opts.QAHistoryID = qaHistoryID
	opts.InitialPrompt = "Ok add a sign to it"

	result2, err := orch.Run(context.Background(), opts)
	r.NoError(err)
	r.Equal(IntentTask, result2.Intent)
	r.NotEmpty(result2.CoderHistoryID)

	// Verify the coder loaded history from Q&A.
	// Load session messages for the coder history — they should include
	// the Q&A conversation messages.
	msgs, err := opts.SessionStore.Load(result2.CoderHistoryID)
	r.NoError(err)
	// Should have: Q&A user + Q&A assistant + augmented user + coder assistant = at least 4
	r.GreaterOrEqual(len(msgs), 4,
		"Coder should have Q&A history + transition message + response")
}

func TestOrchestrator_InvestigateToTaskTransitionCarriesHistory(t *testing.T) {
	r := require.New(t)

	prov := &mockProvider{
		responses: map[string][]types.ChatDelta{},
	}
	for _, m := range testLightweightModels {
		prov.responses[m] = []types.ChatDelta{
			{Type: "text_delta", Text: `{"intent":"investigate","size":"","spec_match":""}`},
		}
	}
	prov.responses["test-model"] = []types.ChatDelta{
		{Type: "text_delta", Text: "The root cause is in the HVAC controller."},
		{Type: "usage", Usage: &types.TokenUsage{InputTokens: 100, OutputTokens: 50}},
		{Type: "message_stop", StopReason: "end_turn"},
	}

	sessDir := t.TempDir()
	store := session.NewStore(sessDir)
	opts := OrchestratorOpts{
		Provider:     prov,
		Registry:     tools.NewRegistry(),
		Bundle:       types.ContextBundle{AgentDefinitions: map[string]types.AgentDefinition{}},
		CWD:          t.TempDir(),
		SessionStore: store,
		SessionID:    "greendale-hvac-session",
		Model:        "test-model",
		Emit:         func(types.OutboundEvent) {},
	}
	opts.InitialPrompt = "Investigate the AC repair school conspiracy"
	orch := NewSWEOrchestrator()

	// First: investigate.
	result, err := orch.Run(context.Background(), opts)
	r.NoError(err)
	r.Equal(IntentInvestigate, result.Intent)
	r.NotEmpty(result.InvestigateHistoryID)

	// Switch to task.
	for _, m := range testLightweightModels {
		prov.responses[m] = []types.ChatDelta{
			{Type: "text_delta", Text: `{"intent":"task","size":"small","spec_match":""}`},
		}
	}

	opts.InvestigateHistoryID = result.InvestigateHistoryID
	opts.InitialPrompt = "Fix the HVAC controller"

	result2, err := orch.Run(context.Background(), opts)
	r.NoError(err)
	r.Equal(IntentTask, result2.Intent)

	// Coder should have investigation history.
	msgs, err := store.Load(result2.CoderHistoryID)
	r.NoError(err)
	r.GreaterOrEqual(len(msgs), 4,
		"Coder should have investigation history + transition message + response")
}
