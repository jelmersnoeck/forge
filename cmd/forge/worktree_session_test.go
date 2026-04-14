package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWriteReadSessionFile(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	info := SessionInfo{
		SessionID: "20260409-quick-flame",
		Branch:    "jelmer/20260409-quick-flame",
		RepoRoot:  "/Users/jeff/Projects/forge",
		CreatedAt: time.Date(2026, 4, 9, 14, 30, 0, 0, time.UTC),
	}

	r.NoError(writeSessionFile(dir, info))

	// File should exist
	r.FileExists(filepath.Join(dir, forgeSessionFile))

	// Round-trip
	got, err := readSessionFile(dir)
	r.NoError(err)
	r.Equal(info.SessionID, got.SessionID)
	r.Equal(info.Branch, got.Branch)
	r.Equal(info.RepoRoot, got.RepoRoot)
	r.Equal(info.CreatedAt.Unix(), got.CreatedAt.Unix())
	r.Equal(dir, got.WorktreePath)
}

func TestReadSessionFile_Missing(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	_, err := readSessionFile(dir)
	r.Error(err)
}

func TestSessionFilePath(t *testing.T) {
	r := require.New(t)

	tests := map[string]struct {
		sessionID string
		wantErr   bool
		wantPath  string
	}{
		"valid session ID": {
			sessionID: "20260414-warm-bloom",
			wantPath:  filepath.Join(defaultSessionsDir, "20260414-warm-bloom.jsonl"),
		},
		"path traversal with ../": {
			sessionID: "../etc/passwd",
			wantErr:   true,
		},
		"path traversal with subdirectory": {
			sessionID: "foo/bar",
			wantErr:   true,
		},
		"dot": {
			sessionID: ".",
			wantErr:   true,
		},
		"double dot": {
			sessionID: "..",
			wantErr:   true,
		},
		"empty string": {
			sessionID: "",
			wantErr:   true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := sessionFilePath(tc.sessionID)
			if tc.wantErr {
				r.Error(err)
				return
			}
			r.NoError(err)
			r.Equal(tc.wantPath, got)
		})
	}
}

func TestSessionFilePath_RespectsEnvVar(t *testing.T) {
	r := require.New(t)

	t.Setenv("SESSIONS_DIR", "/custom/sessions")

	got, err := sessionFilePath("troy-barnes-session")
	r.NoError(err)
	r.Equal("/custom/sessions/troy-barnes-session.jsonl", got)
}

func TestSessionsDir_Default(t *testing.T) {
	r := require.New(t)

	t.Setenv("SESSIONS_DIR", "")
	r.Equal(defaultSessionsDir, sessionsDir())
}

func TestSessionsDir_EnvOverride(t *testing.T) {
	r := require.New(t)

	t.Setenv("SESSIONS_DIR", "/greendale/community/college")
	r.Equal("/greendale/community/college", sessionsDir())
}
