package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestStore_AppendAndLoad(t *testing.T) {
	r := require.New(t)

	dir := t.TempDir()
	store := NewStore(dir)

	sessionID := "troy-barnes-session"

	// Append first message
	msg1 := types.SessionMessage{
		UUID:      "msg-1",
		SessionID: sessionID,
		Type:      "user",
		Message: map[string]any{
			"text": "Hello from Greendale",
		},
		Timestamp: time.Now().Unix(),
	}

	err := store.Append(sessionID, msg1)
	r.NoError(err)

	// Append second message
	msg2 := types.SessionMessage{
		UUID:       "msg-2",
		ParentUUID: "msg-1",
		SessionID:  sessionID,
		Type:       "assistant",
		Message: map[string]any{
			"text": "Welcome to the Human Being mascot fan club!",
		},
		Timestamp: time.Now().Unix(),
	}

	err = store.Append(sessionID, msg2)
	r.NoError(err)

	// Load messages
	messages, err := store.Load(sessionID)
	r.NoError(err)
	r.Len(messages, 2)

	r.Equal("msg-1", messages[0].UUID)
	r.Equal("user", messages[0].Type)

	r.Equal("msg-2", messages[1].UUID)
	r.Equal("msg-1", messages[1].ParentUUID)
	r.Equal("assistant", messages[1].Type)
}

func TestStore_LoadNonexistent(t *testing.T) {
	r := require.New(t)

	dir := t.TempDir()
	store := NewStore(dir)

	// Load from nonexistent session
	messages, err := store.Load("abed-nadir-session")
	r.NoError(err)
	r.Empty(messages)
}

func TestStore_CreatesDirectory(t *testing.T) {
	r := require.New(t)

	dir := filepath.Join(t.TempDir(), "nested", "sessions")
	store := NewStore(dir)

	msg := types.SessionMessage{
		UUID:      "msg-1",
		SessionID: "dean-pelton-session",
		Type:      "user",
		Message:   map[string]any{"text": "Dean-a-ling-a-ling!"},
		Timestamp: time.Now().Unix(),
	}

	err := store.Append("dean-pelton-session", msg)
	r.NoError(err)

	// Verify directory was created
	messages, err := store.Load("dean-pelton-session")
	r.NoError(err)
	r.Len(messages, 1)
}
