package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jelmersnoeck/forge/internal/discord"
	"github.com/jelmersnoeck/forge/internal/forge"
	"github.com/jelmersnoeck/forge/internal/types"
)

// Bridge connects Discord threads to Forge sessions.
type Bridge struct {
	forge   forge.Client
	discord discord.Client
	cfg     *Config
	logger  *slog.Logger

	sessions *SessionMap
	dedup    *EventDedup
	outbox   *RetryQueue

	// Active session translators, keyed by threadID
	mu          sync.Mutex
	translators map[string]*Translator
	cancelFns   map[string]context.CancelFunc
}

// New creates a new Bridge.
func New(f forge.Client, d discord.Client, cfg *Config, logger *slog.Logger) *Bridge {
	return &Bridge{
		forge:       f,
		discord:     d,
		cfg:         cfg,
		logger:      logger,
		sessions:    NewSessionMap(),
		dedup:       NewEventDedup(),
		outbox:      NewRetryQueue(logger),
		translators: make(map[string]*Translator),
		cancelFns:   make(map[string]context.CancelFunc),
	}
}

// Run starts the bridge event loop. Blocks until ctx is cancelled.
func (b *Bridge) Run(ctx context.Context) error {
	// Rebuild session map from Discord pinned messages
	if err := b.rebuildSessions(ctx); err != nil {
		b.logger.Error("failed to rebuild sessions", "error", err)
	}

	// Start retry queue drain goroutine
	go b.outbox.Drain(ctx, b.discord)

	events, err := b.discord.SubscribeEvents(ctx)
	if err != nil {
		return fmt.Errorf("subscribe discord events: %w", err)
	}

	for {
		select {
		case evt, ok := <-events:
			if !ok {
				return nil
			}
			if err := b.OnDiscordEvent(ctx, evt); err != nil {
				b.logger.Error("discord event error", "type", evt.Type, "error", err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// rebuildSessions scans configured channels and rebuilds in-memory state.
func (b *Bridge) rebuildSessions(ctx context.Context) error {
	channelIDs := b.cfg.ChannelIDs()
	if err := b.sessions.Rebuild(ctx, b.discord, channelIDs, b.logger); err != nil {
		return err
	}

	// Start SSE relays for all rebuilt sessions
	entries := b.sessions.Entries()
	for threadID, sessionID := range entries {
		b.logger.Info("resuming session", "thread", threadID, "session", sessionID)
		b.startSSERelay(ctx, threadID, sessionID)
	}
	b.updateStatus()

	return nil
}

// Sessions returns the session map (for admin API).
func (b *Bridge) Sessions() *SessionMap {
	return b.sessions
}

// OnDiscordEvent handles a Discord event.
func (b *Bridge) OnDiscordEvent(ctx context.Context, evt discord.Event) error {
	// Never respond to our own messages
	if evt.UserID == b.discord.BotUserID() || evt.BotUser {
		return nil
	}

	switch evt.Type {
	case discord.EventThreadCreate:
		return b.onThreadCreate(ctx, evt)
	case discord.EventMessageCreate:
		return b.onMessageCreate(ctx, evt)
	case discord.EventReactionAdd:
		return b.onReactionAdd(ctx, evt)
	case discord.EventThreadUpdate:
		return b.onThreadUpdate(ctx, evt)
	case discord.EventReconnect:
		return b.onReconnect(ctx)
	default:
		return nil
	}
}

func (b *Bridge) onThreadCreate(ctx context.Context, evt discord.Event) error {
	// Only handle threads in configured channels
	if !b.cfg.IsForgeChannel(evt.ChannelID) {
		return nil
	}

	// Check idempotency — thread may already have a session
	if sid := b.sessions.GetByThread(evt.ThreadID); sid != "" {
		b.logger.Info("thread already has session, ignoring duplicate THREAD_CREATE",
			"thread", evt.ThreadID, "session", sid)
		return nil
	}

	// Check user authorization
	if !b.cfg.IsUserAllowed(evt.ChannelID, evt.UserID) {
		b.logger.Info("user not allowed", "user", evt.UserID, "channel", evt.ChannelID)
		return nil
	}

	cc := b.cfg.GetChannelConfig(evt.ChannelID)
	if cc == nil {
		return nil
	}

	// Create Forge session
	metadata := map[string]any{
		"source":             "discord",
		"discord.guildId":    evt.GuildID,
		"discord.channelId":  evt.ChannelID,
		"discord.threadId":   evt.ThreadID,
		"discord.userId":     evt.UserID,
		"discord.username":   evt.Username,
	}

	sessionID, err := b.forge.CreateSession(ctx, cc.RepoPath, metadata)
	if err != nil {
		b.logger.Error("failed to create forge session", "error", err)
		_, _ = b.discord.PostMessage(ctx, evt.ThreadID,
			"⏳ Forge is unreachable. Will retry…")
		return fmt.Errorf("create forge session: %w", err)
	}

	// Pin metadata message for thread↔session mapping
	metaContent := ForgeMetaMessage(sessionID)
	metaMsgID, err := b.discord.PostMessage(ctx, evt.ThreadID, metaContent)
	if err != nil {
		b.logger.Error("failed to post meta message", "error", err)
	} else {
		_ = b.discord.PinMessage(ctx, evt.ThreadID, metaMsgID)
	}

	// Store mapping in memory
	b.sessions.Set(evt.ThreadID, sessionID)

	b.logger.Info("session created",
		"thread", evt.ThreadID, "session", sessionID)

	// Start SSE relay
	b.startSSERelay(ctx, evt.ThreadID, sessionID)

	// Update bot status
	b.updateStatus()

	return nil
}

func (b *Bridge) onMessageCreate(ctx context.Context, evt discord.Event) error {
	if evt.Content == "" {
		return nil
	}

	// Look up session for this thread
	sessionID := b.sessions.GetByThread(evt.ThreadID)
	if sessionID == "" {
		return nil
	}

	// Forward to Forge
	if err := b.forge.SendMessage(ctx, sessionID, evt.Content); err != nil {
		b.logger.Error("failed to send message to forge",
			"session", sessionID, "error", err)
		return err
	}

	return nil
}

func (b *Bridge) onReactionAdd(ctx context.Context, evt discord.Event) error {
	sessionID := b.sessions.GetByThread(evt.ThreadID)
	if sessionID == "" {
		return nil
	}

	switch evt.Emoji {
	case "⏸", "⏸️":
		return b.forge.Interrupt(ctx, sessionID)

	case "🔁":
		b.logger.Info("retry reaction received", "thread", evt.ThreadID)
		return nil

	case "🛑":
		_ = b.forge.Interrupt(ctx, sessionID)
		_ = b.discord.ArchiveThread(ctx, evt.ThreadID)
		b.cancelRelay(evt.ThreadID)
		b.sessions.Delete(evt.ThreadID)
		b.dedup.Drop(sessionID)
		b.updateStatus()
	}

	return nil
}

func (b *Bridge) onThreadUpdate(ctx context.Context, evt discord.Event) error {
	if !evt.ThreadArchived {
		return nil
	}

	sessionID := b.sessions.GetByThread(evt.ThreadID)
	if sessionID == "" {
		return nil
	}

	// Thread archived — interrupt and drop
	_ = b.forge.Interrupt(ctx, sessionID)
	b.cancelRelay(evt.ThreadID)
	b.sessions.Delete(evt.ThreadID)
	b.dedup.Drop(sessionID)
	b.updateStatus()
	return nil
}

func (b *Bridge) onReconnect(ctx context.Context) error {
	b.mu.Lock()
	threads := make([]string, 0, len(b.translators))
	for tid := range b.translators {
		threads = append(threads, tid)
	}
	b.mu.Unlock()

	for _, tid := range threads {
		_ = b.discord.AddReaction(ctx, tid, "", "⚠️")
	}
	return nil
}

// OnForgeEvent handles a Forge SSE event for a specific thread.
func (b *Bridge) OnForgeEvent(ctx context.Context, threadID string, evt types.OutboundEvent) error {
	sessionID := b.sessions.GetByThread(threadID)
	if sessionID == "" {
		return nil
	}

	// Idempotency check via ring buffer
	if evt.ID != "" {
		if b.dedup.Seen(sessionID, evt.ID) {
			return nil
		}
	}

	// Translate
	b.mu.Lock()
	tr, ok := b.translators[threadID]
	b.mu.Unlock()
	if !ok {
		return nil
	}

	actions := tr.Translate(evt)

	// Execute actions
	for _, action := range actions {
		msgID, err := b.executeAction(ctx, action)
		if err != nil {
			b.logger.Error("failed to execute discord action",
				"action", action.Type, "thread", threadID, "error", err)
			continue
		}

		// Track bot message IDs
		if action.Type == ActionPost && msgID != "" {
			tr.SetLastBotMsgID(msgID)
			// Track tool embed messages
			if action.Embed != nil && action.Embed.Footer != nil &&
				action.Embed.Footer.Text == "running…" {
				tr.toolMsgID = msgID
			}
		}
	}

	// Record event for idempotency
	if evt.ID != "" {
		b.dedup.Record(sessionID, evt.ID)
	}

	// Handle session completion
	if evt.Type == "done" {
		b.cancelRelay(threadID)
		b.sessions.Delete(threadID)
		b.dedup.Drop(sessionID)
		b.updateStatus()
	}

	return nil
}

func (b *Bridge) executeAction(ctx context.Context, action DiscordAction) (string, error) {
	switch action.Type {
	case ActionPost:
		var opts []discord.PostOption
		if action.Embed != nil {
			opts = append(opts, discord.WithEmbed(action.Embed))
		}
		if action.Pin {
			opts = append(opts, discord.WithPin())
		}
		return b.discord.PostMessage(ctx, action.ThreadID, action.Content, opts...)

	case ActionEdit:
		return "", b.discord.EditMessage(ctx, action.ThreadID, action.MessageID, action.Content)

	case ActionEditEmbed:
		return "", b.discord.EditMessageEmbed(ctx, action.ThreadID, action.MessageID, action.Embed)

	case ActionReact:
		return "", b.discord.AddReaction(ctx, action.ThreadID, action.MessageID, action.Emoji)

	case ActionRemoveReact:
		return "", b.discord.RemoveReaction(ctx, action.ThreadID, action.MessageID, action.Emoji)

	case ActionPin:
		return "", b.discord.PinMessage(ctx, action.ThreadID, action.MessageID)

	default:
		return "", fmt.Errorf("unknown action type: %d", action.Type)
	}
}

// startSSERelay begins streaming Forge events for a session.
func (b *Bridge) startSSERelay(ctx context.Context, threadID, sessionID string) {
	subCtx, cancel := context.WithCancel(ctx)

	b.mu.Lock()
	tr := NewTranslator(threadID, "", sessionID,
		b.cfg.ShowThinking, b.cfg.RevealSessionID)
	b.translators[threadID] = tr
	b.cancelFns[threadID] = cancel
	b.mu.Unlock()

	go func() {
		defer cancel()

		events, err := b.forge.SubscribeEvents(subCtx, sessionID)
		if err != nil {
			b.logger.Error("failed to subscribe to forge events",
				"session", sessionID, "error", err)
			return
		}

		for {
			select {
			case evt, ok := <-events:
				if !ok {
					b.handleSSEDisconnect(ctx, threadID, sessionID)
					return
				}
				if err := b.OnForgeEvent(subCtx, threadID, evt); err != nil {
					b.logger.Error("forge event error",
						"session", sessionID, "type", evt.Type, "error", err)
				}
			case <-subCtx.Done():
				return
			}
		}
	}()
}

func (b *Bridge) handleSSEDisconnect(ctx context.Context, threadID, sessionID string) {
	if b.sessions.GetByThread(threadID) == "" {
		return
	}

	b.logger.Warn("SSE disconnected, attempting reconnect",
		"session", sessionID, "thread", threadID)

	// Post warning reaction
	b.mu.Lock()
	tr, ok := b.translators[threadID]
	b.mu.Unlock()
	if ok && tr.lastBotMsgID != "" {
		_ = b.discord.AddReaction(ctx, threadID, tr.lastBotMsgID, "⚠️")
	}

	// Reconnect with backoff
	backoff := time.Second
	for i := 0; i < 5; i++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		b.startSSERelay(ctx, threadID, sessionID)
		return
	}
}

func (b *Bridge) cancelRelay(threadID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cancel, ok := b.cancelFns[threadID]; ok {
		cancel()
		delete(b.cancelFns, threadID)
	}
	delete(b.translators, threadID)
}

func (b *Bridge) updateStatus() {
	count := b.sessions.Len()
	status := fmt.Sprintf("Watching: %d sessions", count)
	_ = b.discord.UpdateStatus(context.Background(), status)
}

// ActiveSessionCount returns the number of active sessions.
func (b *Bridge) ActiveSessionCount() int {
	return b.sessions.Len()
}
