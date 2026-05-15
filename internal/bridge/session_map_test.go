package bridge

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/jelmersnoeck/forge/internal/discord"
	"github.com/stretchr/testify/require"
)

func TestParseForgeMetaSession(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
	}{
		"valid block": {
			input: "```forge-meta\nsession: abc-123\n```",
			want:  "abc-123",
		},
		"with surrounding text": {
			input: "Some preamble\n```forge-meta\nsession: sess-xyz\n```\nSome trailing",
			want:  "sess-xyz",
		},
		"with extra whitespace": {
			input: "```forge-meta\n  session:   my-session-id  \n```",
			want:  "my-session-id",
		},
		"no forge-meta block": {
			input: "Just a regular message",
			want:  "",
		},
		"wrong fence language": {
			input: "```json\nsession: abc\n```",
			want:  "",
		},
		"empty session": {
			input: "```forge-meta\nsession: \n```",
			want:  "",
		},
		"uuid session id": {
			input: "```forge-meta\nsession: 550e8400-e29b-41d4-a716-446655440000\n```",
			want:  "550e8400-e29b-41d4-a716-446655440000",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := ParseForgeMetaSession(tc.input)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestForgeMetaMessage(t *testing.T) {
	msg := ForgeMetaMessage("session-42")
	require.Equal(t, "```forge-meta\nsession: session-42\n```", msg)

	// Round-trip: generate → parse
	parsed := ParseForgeMetaSession(msg)
	require.Equal(t, "session-42", parsed)
}

func TestSessionMap_BasicOperations(t *testing.T) {
	m := NewSessionMap()

	m.Set("thread-1", "session-a")
	m.Set("thread-2", "session-b")

	require.Equal(t, "session-a", m.GetByThread("thread-1"))
	require.Equal(t, "session-b", m.GetByThread("thread-2"))
	require.Equal(t, "", m.GetByThread("thread-3"))

	require.Equal(t, "thread-1", m.GetBySession("session-a"))
	require.Equal(t, 2, m.Len())

	m.Delete("thread-1")
	require.Equal(t, "", m.GetByThread("thread-1"))
	require.Equal(t, "", m.GetBySession("session-a"))
	require.Equal(t, 1, m.Len())
}

func TestSessionMap_Rebuild(t *testing.T) {
	dc := discord.NewStubClient("bot-1")

	// Set up threads and pins
	dc.Threads["channel-1"] = []discord.ThreadInfo{
		{ID: "thread-1", ParentID: "channel-1", Archived: false},
		{ID: "thread-2", ParentID: "channel-1", Archived: false},
		{ID: "thread-3", ParentID: "channel-1", Archived: true}, // archived, should be skipped
	}
	dc.Threads["channel-2"] = []discord.ThreadInfo{
		{ID: "thread-4", ParentID: "channel-2", Archived: false},
	}

	dc.PinnedMsgs["thread-1"] = []discord.PinnedMessage{
		{ID: "pin-1", Content: "```forge-meta\nsession: sess-abc\n```"},
	}
	dc.PinnedMsgs["thread-2"] = []discord.PinnedMessage{
		{ID: "pin-2", Content: "Just a regular pinned message"},
	}
	dc.PinnedMsgs["thread-4"] = []discord.PinnedMessage{
		{ID: "pin-3", Content: "other stuff"},
		{ID: "pin-4", Content: "```forge-meta\nsession: sess-def\n```"},
	}

	m := NewSessionMap()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	err := m.Rebuild(context.Background(), dc, []string{"channel-1", "channel-2"}, logger)
	require.NoError(t, err)

	// thread-1 has session
	require.Equal(t, "sess-abc", m.GetByThread("thread-1"))

	// thread-2 has no forge-meta pin
	require.Equal(t, "", m.GetByThread("thread-2"))

	// thread-3 is archived, skipped
	require.Equal(t, "", m.GetByThread("thread-3"))

	// thread-4 has session (second pin matched)
	require.Equal(t, "sess-def", m.GetByThread("thread-4"))

	require.Equal(t, 2, m.Len())
}

func TestSessionMap_Rebuild_ClearsExisting(t *testing.T) {
	m := NewSessionMap()
	m.Set("old-thread", "old-session")

	dc := discord.NewStubClient("bot-1")
	dc.Threads["ch-1"] = []discord.ThreadInfo{
		{ID: "new-thread", ParentID: "ch-1"},
	}
	dc.PinnedMsgs["new-thread"] = []discord.PinnedMessage{
		{ID: "p1", Content: "```forge-meta\nsession: new-session\n```"},
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	err := m.Rebuild(context.Background(), dc, []string{"ch-1"}, logger)
	require.NoError(t, err)

	// Old mapping should be gone
	require.Equal(t, "", m.GetByThread("old-thread"))
	require.Equal(t, "new-session", m.GetByThread("new-thread"))
}

func TestSessionMap_Entries(t *testing.T) {
	m := NewSessionMap()
	m.Set("t1", "s1")
	m.Set("t2", "s2")

	entries := m.Entries()
	require.Len(t, entries, 2)
	require.Equal(t, "s1", entries["t1"])
	require.Equal(t, "s2", entries["t2"])
}

func TestSessionMap_ThreadIDs(t *testing.T) {
	m := NewSessionMap()
	m.Set("t1", "s1")
	m.Set("t2", "s2")

	ids := m.ThreadIDs()
	require.Len(t, ids, 2)
	require.ElementsMatch(t, []string{"t1", "t2"}, ids)
}
