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
	r.FileExists(filepath.Join(dir, RelPath()))

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

	r.NoError(os.MkdirAll(filepath.Join(dir, StateDir), 0o755))
	r.NoError(os.WriteFile(filepath.Join(dir, RelPath()),
		[]byte(`{"version":999,"sessionID":"señor-chang"}`), 0o644))

	_, err := Read(dir)
	r.Error(err)
	r.NotErrorIs(err, os.ErrNotExist)
}

func TestRead_Corrupt(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	r.NoError(os.MkdirAll(filepath.Join(dir, StateDir), 0o755))
	r.NoError(os.WriteFile(filepath.Join(dir, RelPath()),
		[]byte(`{not json`), 0o644))

	_, err := Read(dir)
	r.Error(err)
}

func TestWrite_Atomic_NoTmpLeftover(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	r.NoError(Write(dir, State{SessionID: "human-being"}))

	_, err := os.Stat(filepath.Join(dir, RelPath()+".tmp"))
	r.ErrorIs(err, os.ErrNotExist)
}

func TestWrite_SeedsGitignore(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	r.NoError(Write(dir, State{SessionID: "greendale"}))

	data, err := os.ReadFile(filepath.Join(dir, StateDir, ".gitignore"))
	r.NoError(err)
	r.Equal(StateFile+"\n", string(data))
}

func TestWrite_GitignoreIdempotent(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	r.NoError(Write(dir, State{SessionID: "abed"}))
	gi := filepath.Join(dir, StateDir, ".gitignore")
	first, err := os.ReadFile(gi)
	r.NoError(err)

	r.NoError(Write(dir, State{SessionID: "abed"}))
	second, err := os.ReadFile(gi)
	r.NoError(err)

	r.Equal(first, second) // byte-identical, no duplicate line
}

func TestWrite_GitignorePreservesExisting(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()
	r.NoError(os.MkdirAll(filepath.Join(dir, StateDir), 0o755))
	gi := filepath.Join(dir, StateDir, ".gitignore")
	r.NoError(os.WriteFile(gi, []byte("settings.local.json\n"), 0o644))

	r.NoError(Write(dir, State{SessionID: "troy"}))

	data, err := os.ReadFile(gi)
	r.NoError(err)
	r.Equal("settings.local.json\n"+StateFile+"\n", string(data))
}

func TestWrite_GitignoreAppendsNewlineWhenMissing(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()
	r.NoError(os.MkdirAll(filepath.Join(dir, StateDir), 0o755))
	gi := filepath.Join(dir, StateDir, ".gitignore")
	r.NoError(os.WriteFile(gi, []byte("settings.local.json"), 0o644)) // no trailing newline

	r.NoError(Write(dir, State{SessionID: "annie"}))

	data, err := os.ReadFile(gi)
	r.NoError(err)
	r.Equal("settings.local.json\n"+StateFile+"\n", string(data))
}

func TestRead_LegacyFallback(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	// Legacy root-level file, no .forge/ state.
	r.NoError(os.WriteFile(filepath.Join(dir, StateFile),
		[]byte(`{"version":1,"sessionID":"legacy-chang"}`), 0o644))

	got, err := Read(dir)
	r.NoError(err)
	r.Equal("legacy-chang", got.SessionID)
}

func TestRead_LegacyMigratesOnWrite(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()
	legacy := filepath.Join(dir, StateFile)
	r.NoError(os.WriteFile(legacy,
		[]byte(`{"version":1,"sessionID":"legacy-troy"}`), 0o644))

	got, err := Read(dir)
	r.NoError(err)

	r.NoError(Write(dir, got))
	r.FileExists(filepath.Join(dir, RelPath()))
	r.FileExists(legacy) // legacy not deleted (no destructive migration)
}

func TestRead_PrefersNewOverLegacy(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()
	r.NoError(os.WriteFile(filepath.Join(dir, StateFile),
		[]byte(`{"version":1,"sessionID":"legacy"}`), 0o644))
	r.NoError(Write(dir, State{SessionID: "new-location"}))

	got, err := Read(dir)
	r.NoError(err)
	r.Equal("new-location", got.SessionID)
}

func TestWrite_ForgeIsFile_Errors(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()
	// .forge exists as a file, not a directory.
	r.NoError(os.WriteFile(filepath.Join(dir, StateDir), []byte("x"), 0o644))

	err := Write(dir, State{SessionID: "chang"})
	r.Error(err)
}
