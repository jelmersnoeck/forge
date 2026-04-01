package session

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriter_WriteRead(t *testing.T) {
	r := require.New(t)
	
	tmpDir := t.TempDir()
	sessionID := "test-session-123"
	
	// Write entries
	w, err := NewWriter(sessionID, tmpDir)
	r.NoError(err)
	defer w.Close()
	
	uuid1, err := w.WriteUser("Hello, agent!")
	r.NoError(err)
	r.NotEmpty(uuid1)
	
	uuid2, err := w.WriteAssistant([]ContentBlock{
		{Type: "text", Text: "Hello! How can I help?"},
	}, "end_turn", nil)
	r.NoError(err)
	r.NotEmpty(uuid2)
	
	uuid3, err := w.WriteSystem("Task completed", false)
	r.NoError(err)
	r.NotEmpty(uuid3)
	
	r.NoError(w.Close())
	
	// Read entries back
	reader := NewReader(sessionID, tmpDir)
	entries, err := reader.ReadAll()
	r.NoError(err)
	r.Len(entries, 3)
	
	// Check chain
	r.Equal(uuid1, entries[0].UUID)
	r.Equal("", entries[0].ParentUUID)
	r.Equal(uuid2, entries[1].UUID)
	r.Equal(uuid1, entries[1].ParentUUID)
	r.Equal(uuid3, entries[2].UUID)
	r.Equal(uuid2, entries[2].ParentUUID)
}

func TestWriter_ProgressEntriesAreSkipped(t *testing.T) {
	r := require.New(t)
	
	tmpDir := t.TempDir()
	sessionID := "test-progress"
	
	w, err := NewWriter(sessionID, tmpDir)
	r.NoError(err)
	defer w.Close()
	
	_, err = w.WriteUser("Start task")
	r.NoError(err)
	
	_, err = w.WriteProgress("tool-123", "Bash", map[string]any{"output": "running..."})
	r.NoError(err)
	
	_, err = w.WriteAssistant([]ContentBlock{
		{Type: "text", Text: "Task complete"},
	}, "end_turn", nil)
	r.NoError(err)
	
	r.NoError(w.Close())
	
	// Read back - progress entries should be skipped
	reader := NewReader(sessionID, tmpDir)
	entries, err := reader.ReadAll()
	r.NoError(err)
	r.Len(entries, 2) // Only user + assistant
}

func TestValidateChain(t *testing.T) {
	tests := map[string]struct {
		entries []Entry
		wantErr bool
	}{
		"valid chain": {
			entries: []Entry{
				{UUID: "a", ParentUUID: ""},
				{UUID: "b", ParentUUID: "a"},
				{UUID: "c", ParentUUID: "b"},
			},
			wantErr: false,
		},
		"first entry has parent": {
			entries: []Entry{
				{UUID: "a", ParentUUID: "invalid"},
			},
			wantErr: true,
		},
		"missing parent": {
			entries: []Entry{
				{UUID: "a", ParentUUID: ""},
				{UUID: "b", ParentUUID: "nonexistent"},
			},
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			
			brokenEntry := ValidateChain(tc.entries)
			if tc.wantErr {
				r.NotNil(brokenEntry)
			} else {
				r.Nil(brokenEntry)
			}
		})
	}
}
