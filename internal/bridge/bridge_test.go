package bridge

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/jelmersnoeck/forge/internal/discord"
	"github.com/jelmersnoeck/forge/internal/forge"
	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func setupBridge(t *testing.T) (*Bridge, *discord.StubClient, *forge.StubClient) {
	t.Helper()

	dc := discord.NewStubClient("bot-123")
	fc := forge.NewStubClient()

	cfg := &Config{
		GuildID:         "guild-1",
		ForgeGatewayURL: "http://localhost:3000",
	}
	cfg.SetChannelsForTest(ChannelsConfig{
		Channels: []ChannelConfig{
			{
				ChannelID:         "channel-1",
				RepoPath:          "/code/forge",
				DefaultBaseBranch: "main",
			},
		},
	})

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	b := New(fc, dc, cfg, logger)

	return b, dc, fc
}

func TestBridge_ThreadCreate_CreatesSession(t *testing.T) {
	b, dc, fc := setupBridge(t)
	ctx := context.Background()

	err := b.OnDiscordEvent(ctx, discord.Event{
		Type:      discord.EventThreadCreate,
		GuildID:   "guild-1",
		ChannelID: "channel-1",
		ThreadID:  "thread-1",
		UserID:    "user-1",
		Username:  "troy.barnes",
	})
	require.NoError(t, err)

	// Forge session created
	sessions := fc.GetSessions()
	require.Len(t, sessions, 1)
	require.Equal(t, "/code/forge", sessions[0].CWD)
	require.Equal(t, "discord", sessions[0].Metadata["source"])

	// In-memory mapping created
	sid := b.sessions.GetByThread("thread-1")
	require.NotEmpty(t, sid)

	// Pinned meta message posted
	msgs := dc.GetMessages()
	var foundMeta bool
	for _, m := range msgs {
		if ParseForgeMetaSession(m.Content) != "" {
			foundMeta = true
		}
	}
	require.True(t, foundMeta, "should post forge-meta message")
}

func TestBridge_ThreadCreate_Idempotent(t *testing.T) {
	b, _, fc := setupBridge(t)
	ctx := context.Background()

	evt := discord.Event{
		Type:      discord.EventThreadCreate,
		ChannelID: "channel-1",
		ThreadID:  "thread-1",
		UserID:    "user-1",
	}

	require.NoError(t, b.OnDiscordEvent(ctx, evt))
	require.NoError(t, b.OnDiscordEvent(ctx, evt)) // duplicate

	// Only one session created
	require.Len(t, fc.GetSessions(), 1)
}

func TestBridge_ThreadCreate_IgnoresNonForgeChannel(t *testing.T) {
	b, _, fc := setupBridge(t)
	ctx := context.Background()

	err := b.OnDiscordEvent(ctx, discord.Event{
		Type:      discord.EventThreadCreate,
		ChannelID: "random-channel",
		ThreadID:  "thread-1",
		UserID:    "user-1",
	})
	require.NoError(t, err)
	require.Empty(t, fc.GetSessions())
}

func TestBridge_ThreadCreate_IgnoresBotUser(t *testing.T) {
	b, _, fc := setupBridge(t)
	ctx := context.Background()

	err := b.OnDiscordEvent(ctx, discord.Event{
		Type:      discord.EventThreadCreate,
		ChannelID: "channel-1",
		ThreadID:  "thread-1",
		UserID:    "bot-123", // bot's own ID
	})
	require.NoError(t, err)
	require.Empty(t, fc.GetSessions())
}

func TestBridge_MessageCreate_ForwardsToForge(t *testing.T) {
	b, _, fc := setupBridge(t)
	ctx := context.Background()

	// Set up session mapping directly
	b.sessions.Set("thread-1", "session-1")

	err := b.OnDiscordEvent(ctx, discord.Event{
		Type:     discord.EventMessageCreate,
		ThreadID: "thread-1",
		UserID:   "user-1",
		Content:  "Fix the bug in main.go",
	})
	require.NoError(t, err)

	msgs := fc.GetMessages()
	require.Len(t, msgs, 1)
	require.Equal(t, "session-1", msgs[0].SessionID)
	require.Equal(t, "Fix the bug in main.go", msgs[0].Text)
}

func TestBridge_MessageCreate_IgnoresEmptyContent(t *testing.T) {
	b, _, fc := setupBridge(t)
	ctx := context.Background()

	b.sessions.Set("thread-1", "session-1")

	err := b.OnDiscordEvent(ctx, discord.Event{
		Type:     discord.EventMessageCreate,
		ThreadID: "thread-1",
		UserID:   "user-1",
		Content:  "",
	})
	require.NoError(t, err)
	require.Empty(t, fc.GetMessages())
}

func TestBridge_ReactionPause_InterruptsSession(t *testing.T) {
	b, _, fc := setupBridge(t)
	ctx := context.Background()

	b.sessions.Set("thread-1", "session-1")

	err := b.OnDiscordEvent(ctx, discord.Event{
		Type:      discord.EventReactionAdd,
		ThreadID:  "thread-1",
		MessageID: "msg-1",
		UserID:    "user-1",
		Emoji:     "⏸️",
	})
	require.NoError(t, err)

	interrupts := fc.GetInterrupts()
	require.Len(t, interrupts, 1)
	require.Equal(t, "session-1", interrupts[0])
}

func TestBridge_ReactionStop_ArchivesThread(t *testing.T) {
	b, dc, fc := setupBridge(t)
	ctx := context.Background()

	b.sessions.Set("thread-1", "session-1")

	err := b.OnDiscordEvent(ctx, discord.Event{
		Type:      discord.EventReactionAdd,
		ThreadID:  "thread-1",
		MessageID: "starter-msg",
		UserID:    "user-1",
		Emoji:     "🛑",
	})
	require.NoError(t, err)

	// Session interrupted and archived
	require.NotEmpty(t, fc.GetInterrupts())
	require.Contains(t, dc.GetArchives(), "thread-1")

	// Mapping removed
	require.Empty(t, b.sessions.GetByThread("thread-1"))
}

func TestBridge_OnForgeEvent_IdempotentEvents(t *testing.T) {
	b, dc, _ := setupBridge(t)
	ctx := context.Background()

	b.sessions.Set("thread-1", "session-1")

	// Register translator
	b.mu.Lock()
	b.translators["thread-1"] = NewTranslator("thread-1", "", "session-1", false, false)
	b.mu.Unlock()

	evt := types.OutboundEvent{
		ID:      "evt-1",
		Type:    "text",
		Content: "Hello from Forge",
	}

	// First delivery
	require.NoError(t, b.OnForgeEvent(ctx, "thread-1", evt))

	// Replay same event within the same session — should be deduped
	require.NoError(t, b.OnForgeEvent(ctx, "thread-1", evt))

	// Flush with done — only the first text + done summary
	doneEvt := types.OutboundEvent{ID: "evt-2", Type: "done"}
	require.NoError(t, b.OnForgeEvent(ctx, "thread-1", doneEvt))

	msgCount := len(dc.GetMessages())

	// Text was buffered only once (deduped), then flushed on done → 1 text + 1 done embed = 2
	require.Equal(t, 2, msgCount, "duplicate events within a session should be deduped")
}

func TestBridge_OnForgeEvent_EmptyID_NotDeduped(t *testing.T) {
	b, dc, _ := setupBridge(t)
	ctx := context.Background()

	b.sessions.Set("thread-1", "session-1")
	b.mu.Lock()
	b.translators["thread-1"] = NewTranslator("thread-1", "", "session-1", false, false)
	b.mu.Unlock()

	// Events with empty IDs should not be deduped
	evt := types.OutboundEvent{
		ID:      "",
		Type:    "text",
		Content: "No ID event",
	}

	require.NoError(t, b.OnForgeEvent(ctx, "thread-1", evt))
	require.NoError(t, b.OnForgeEvent(ctx, "thread-1", evt))

	// Both events buffer text, flush on done
	doneEvt := types.OutboundEvent{Type: "done"}
	require.NoError(t, b.OnForgeEvent(ctx, "thread-1", doneEvt))

	msgs := dc.GetMessages()
	// Should have text post(s) + done embed
	require.GreaterOrEqual(t, len(msgs), 2)
}

func TestBridge_ThreadUpdate_Archived_DropsSession(t *testing.T) {
	b, _, fc := setupBridge(t)
	ctx := context.Background()

	b.sessions.Set("thread-1", "session-1")

	err := b.OnDiscordEvent(ctx, discord.Event{
		Type:           discord.EventThreadUpdate,
		ThreadID:       "thread-1",
		ThreadArchived: true,
	})
	require.NoError(t, err)

	require.NotEmpty(t, fc.GetInterrupts())
	require.Empty(t, b.sessions.GetByThread("thread-1"))
}

// SetChannelsForTest is a test helper on Config.
func (c *Config) SetChannelsForTest(cfg ChannelsConfig) {
	c.mu.Lock()
	c.channels = cfg
	c.mu.Unlock()
}
