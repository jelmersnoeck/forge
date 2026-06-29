package sessionstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWriteRead_RoundTrip(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	want := State{
		SessionID:        "20260409-quick-flame",
		Phase:            "orchestrator",
		HistoryID:        "troy-barnes-coder",
		QAHistoryID:      "abed-nadir-qa",
		InvestigateID:    "annie-edison-investigate",
		OrchestratorDone: true,
		HeadCommit:       "abc1234",
		UpdatedAt:        time.Date(2026, 4, 9, 15, 30, 0, 0, time.UTC),
	}

	r.NoError(Write(dir, want))
	r.FileExists(filepath.Join(dir, StateFile))

	got, err := Read(dir)
	r.NoError(err)
	r.Equal(Version, got.Version)
	r.Equal(want.SessionID, got.SessionID)
	r.Equal(want.Phase, got.Phase)
	r.Equal(want.HistoryID, got.HistoryID)
	r.Equal(want.QAHistoryID, got.QAHistoryID)
	r.Equal(want.InvestigateID, got.InvestigateID)
	r.True(got.OrchestratorDone)
	r.Equal(want.HeadCommit, got.HeadCommit)
	r.Equal(want.UpdatedAt.Unix(), got.UpdatedAt.Unix())
}

func TestWrite_StampsVersionAndTime(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	r.NoError(Write(dir, State{SessionID: "greendale"}))

	got, err := Read(dir)
	r.NoError(err)
	r.Equal(Version, got.Version)
	r.False(got.UpdatedAt.IsZero())
}

func TestRead_Missing(t *testing.T) {
	r := require.New(t)
	_, err := Read(t.TempDir())
	r.ErrorIs(err, os.ErrNotExist)
}

func TestRead_UnknownVersion(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	r.NoError(os.WriteFile(filepath.Join(dir, StateFile),
		[]byte(`{"version":999,"sessionID":"señor-chang"}`), 0o644))

	_, err := Read(dir)
	r.Error(err)
	r.NotErrorIs(err, os.ErrNotExist)
}

func TestRead_Corrupt(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	r.NoError(os.WriteFile(filepath.Join(dir, StateFile),
		[]byte(`{not json`), 0o644))

	_, err := Read(dir)
	r.Error(err)
}

func TestWrite_Atomic_NoTmpLeftover(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	r.NoError(Write(dir, State{SessionID: "human-being"}))

	_, err := os.Stat(filepath.Join(dir, StateFile+".tmp"))
	r.ErrorIs(err, os.ErrNotExist)
}
