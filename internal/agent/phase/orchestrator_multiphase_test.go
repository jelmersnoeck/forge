package phase

import (
	"context"
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

// noopSpawn is a SpawnPhaseAgent that records nothing and always succeeds.
func noopSpawn(_ context.Context, _, _, _ string) error { return nil }

func TestRunMultiPhasePipeline_ExistingSubIssues_SkipsDecompose(t *testing.T) {
	r := require.New(t)

	wantPhases := []SubIssue{
		{Number: 11, Title: "Greendale foundation", Body: "lay bricks"},
		{Number: 12, Title: "Greendale roof", Body: "add tiles"},
	}

	var got MultiPhaseOpts
	o := &Orchestrator{
		maxReviewCycles: maxReviewCycles,
		runMultiPhase: func(_ context.Context, opts MultiPhaseOpts) error {
			got = opts
			return nil
		},
	}

	opts := OrchestratorOpts{
		MultiPhase:        true,
		IssueNumber:       42,
		IssueTitle:        "Build Greendale",
		IssueBody:         "the whole campus",
		CWD:               t.TempDir(),
		Emit:              func(types.OutboundEvent) {},
		SpawnPhaseAgentFn: noopSpawn,
		SubIssuesFn: func(_ context.Context, _ string, parent int) ([]SubIssue, error) {
			r.Equal(42, parent)
			return wantPhases, nil
		},
	}

	_, err := o.runMultiPhasePipeline(context.Background(), opts)
	r.NoError(err)
	r.Equal(wantPhases, got.Phases)
	// Parent title slugified into the branch prefix.
	r.Equal("build-greendale", got.ParentSlug)
}

func TestRunMultiPhasePipeline_NoSubIssues_DecomposesAndCreates(t *testing.T) {
	r := require.New(t)

	// LLM returns one phase; CreateSubIssuesFn is injected so the test stays
	// hermetic (no gh). The SubIssuesFn returns nothing on the first call
	// (none exist) then the created phases on the second (post-create).
	prov := &mockProvider{
		responses: map[string][]types.ChatDelta{
			testLightweightModels[0]: {
				{Type: "text_delta", Text: `[{"title":"Phase A","body":"a","depends_on":[]}]`},
			},
		},
	}

	calls := 0
	created := []SubIssue{{Number: 99, Title: "Phase A", Body: "a"}}

	var ranMulti bool
	o := &Orchestrator{
		maxReviewCycles: maxReviewCycles,
		runMultiPhase: func(_ context.Context, opts MultiPhaseOpts) error {
			ranMulti = true
			r.Equal(created, opts.Phases)
			return nil
		},
	}

	opts := OrchestratorOpts{
		MultiPhase:        true,
		IssueNumber:       7,
		IssueBody:         "decompose me into shippable phases",
		Provider:          prov,
		CWD:               t.TempDir(),
		Emit:              func(types.OutboundEvent) {},
		SpawnPhaseAgentFn: noopSpawn,
		SubIssuesFn: func(_ context.Context, _ string, _ int) ([]SubIssue, error) {
			calls++
			if calls == 1 {
				return nil, nil // none exist yet
			}
			return created, nil // after decompose+create
		},
		CreateSubIssuesFn: func(_ context.Context, parent int, _ string, tasks []SubTask) ([]int, error) {
			r.Equal(7, parent)
			r.Len(tasks, 1)
			return []int{99}, nil
		},
	}

	_, err := o.runMultiPhasePipeline(context.Background(), opts)
	r.NoError(err)
	r.True(ranMulti)
	r.Equal(2, calls) // fetched once before, once after create
}

func TestRunMultiPhasePipeline_RequiresSpawnAgent(t *testing.T) {
	r := require.New(t)
	o := &Orchestrator{maxReviewCycles: maxReviewCycles}
	_, err := o.runMultiPhasePipeline(context.Background(), OrchestratorOpts{
		MultiPhase:  true,
		IssueNumber: 1,
		CWD:         t.TempDir(),
		Emit:        func(types.OutboundEvent) {},
		SubIssuesFn: func(_ context.Context, _ string, _ int) ([]SubIssue, error) {
			return []SubIssue{{Number: 2, Title: "p"}}, nil
		},
	})
	r.Error(err)
	r.Contains(err.Error(), "SpawnPhaseAgentFn is required")
}

func TestRunMultiPhasePipeline_FetchError(t *testing.T) {
	r := require.New(t)
	o := &Orchestrator{maxReviewCycles: maxReviewCycles}
	_, err := o.runMultiPhasePipeline(context.Background(), OrchestratorOpts{
		MultiPhase:        true,
		IssueNumber:       1,
		CWD:               t.TempDir(),
		Emit:              func(types.OutboundEvent) {},
		SpawnPhaseAgentFn: noopSpawn,
		SubIssuesFn: func(_ context.Context, _ string, _ int) ([]SubIssue, error) {
			return nil, context.DeadlineExceeded
		},
	})
	r.Error(err)
	r.Contains(err.Error(), "fetch sub-issues")
}

func TestParseGHSubIssues(t *testing.T) {
	r := require.New(t)

	got, err := parseGHSubIssues([]byte(`[
		{"number":3,"title":"Troy","body":"first"},
		{"number":4,"title":"Abed","body":"second"}
	]`))
	r.NoError(err)
	r.Equal([]SubIssue{
		{Number: 3, Title: "Troy", Body: "first"},
		{Number: 4, Title: "Abed", Body: "second"},
	}, got)

	empty, err := parseGHSubIssues([]byte(`[]`))
	r.NoError(err)
	r.Nil(empty)

	_, err = parseGHSubIssues([]byte(`not json`))
	r.Error(err)
}
