package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jelmersnoeck/forge/internal/sessionstate"
	"github.com/stretchr/testify/require"
)

func TestPhaseNameRoundTrip(t *testing.T) {
	r := require.New(t)
	phases := []WorkerPhase{PhaseIdle, PhaseQA, PhaseInvestigate, PhaseTriage, PhaseOrchestrator, PhaseDone}
	for _, p := range phases {
		r.Equal(p, phaseFromName(phaseName(p)))
	}
}

func TestPhaseFromName_Unknown(t *testing.T) {
	require.Equal(t, PhaseIdle, phaseFromName("greendale"))
}

func TestWorker_InitialState_NoFile(t *testing.T) {
	r := require.New(t)
	w := NewWorker(NewHub(), "abed-101", t.TempDir(), t.TempDir(), "swe", "", "", "")

	got := w.initialState()
	r.Equal(WorkerState{}, got)
}

func TestWorker_PersistThenInitialState(t *testing.T) {
	r := require.New(t)
	cwd := t.TempDir()
	w := NewWorker(NewHub(), "troy-101", cwd, t.TempDir(), "swe", "", "", "")

	w.persistState(WorkerState{
		Phase:                PhaseOrchestrator,
		HistoryID:            "coder-hist",
		QAHistoryID:          "qa-hist",
		InvestigateHistoryID: "inv-hist",
		TriageHistoryID:      "triage-hist",
	})

	// Raw file should reflect orchestratorDone and phase label.
	st, err := sessionstate.Read(cwd)
	r.NoError(err)
	r.Equal("orchestrator", st.Phase)
	r.True(st.OrchestratorDone)
	r.Equal("coder-hist", st.HistoryID)
	r.Equal("triage-hist", st.TriageID)

	// initialState reconstructs the WorkerState.
	got := w.initialState()
	r.Equal(PhaseOrchestrator, got.Phase)
	r.Equal("coder-hist", got.HistoryID)
	r.Equal("qa-hist", got.QAHistoryID)
	r.Equal("inv-hist", got.InvestigateHistoryID)
	r.Equal("triage-hist", got.TriageHistoryID)
}

func TestWorker_InitialState_CorruptIsFresh(t *testing.T) {
	r := require.New(t)
	cwd := t.TempDir()
	w := NewWorker(NewHub(), "annie-101", cwd, t.TempDir(), "swe", "", "", "")

	// Tamper with the state file so it fails to parse.
	r.NoError(os.WriteFile(filepath.Join(cwd, sessionstate.StateFile), []byte("{bad"), 0o644))

	r.Equal(WorkerState{}, w.initialState())
}
