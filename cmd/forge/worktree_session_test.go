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
