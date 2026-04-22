package phase

import (
	"fmt"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestMaxFinalizeRecoveries(t *testing.T) {
	r := require.New(t)
	r.Equal(1, maxFinalizeRecoveries, "only 1 recovery attempt allowed per spec")
}

func TestBuildFinalizeRecoveryPrompt(t *testing.T) {
	tests := map[string]struct {
		err          error
		wantContains []string
	}{
		"rebase error": {
			err: fmt.Errorf("rebase onto origin/main failed: cannot rebase with uncommitted changes"),
			wantContains: []string{
				"rebase onto origin/main failed",
				"cannot rebase with uncommitted changes",
				"git state",
				"Do not modify",
			},
		},
		"push error": {
			err: fmt.Errorf("push failed: non-fast-forward"),
			wantContains: []string{
				"push failed",
				"non-fast-forward",
				"git status",
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			prompt := buildFinalizeRecoveryPrompt(tc.err)
			for _, want := range tc.wantContains {
				r.Contains(prompt, want)
			}
		})
	}
}

func TestBuildFinalizeRecoveryPromptPreventsScopeCreep(t *testing.T) {
	r := require.New(t)
	prompt := buildFinalizeRecoveryPrompt(fmt.Errorf("rebase failed: dirty worktree"))

	// The prompt must instruct the LLM to only fix git state.
	r.Contains(prompt, "Do not modify")
	r.Contains(prompt, "code")
	// Must mention committing or resolving as recovery actions.
	r.Contains(prompt, "commit")
}

func TestEmitPhaseError(t *testing.T) {
	r := require.New(t)
	orch := NewSWEOrchestrator()

	var events []types.OutboundEvent
	opts := OrchestratorOpts{
		SessionID: "greendale-101",
		Emit: func(e types.OutboundEvent) {
			events = append(events, e)
		},
	}

	finalizeErr := fmt.Errorf("rebase onto origin/main failed: uncommitted changes exist")
	orch.emitPhaseError(opts, finalizeErr)

	r.Len(events, 1)
	evt := events[0]
	r.Equal("phase_error", evt.Type)
	r.Contains(evt.Content, "finalize:")
	r.Contains(evt.Content, "uncommitted changes exist")
	r.Equal("greendale-101", evt.SessionID)
	r.NotEmpty(evt.ID)
	r.NotZero(evt.Timestamp)
}
