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

	// SSE reconnect tracking, keyed by threadID. Reset to 0 on any successful
	// event delivery (see OnForgeEvent). Incremented on each disconnect.
	// Capped at sseMaxReconnectAttempts — after that we declare the relay
	// dead and stop reconnecting.
	reconnectAttempts map[string]int
	reconnectLastErr  map[string]string // for /metrics surfacing
}

const (
	// sseMaxReconnectAttempts is the maximum number of consecutive failed
	// reconnect attempts before we declare the SSE relay dead and stop
	// reconnecting. With the backoff schedule below this is ~5 minutes of
	// trying before giving up.
	sseMaxReconnectAttempts = 10

	// sseBackoffInitial is the first reconnect delay. Doubles each attempt.
	sseBackoffInitial = time.Second
	// sseBackoffMax caps the exponential backoff.
	sseBackoffMax = 30 * time.Second
)

// sseBackoffFor returns the backoff duration for the given attempt number
// (1-indexed). Schedule: 1s, 2s, 4s, 8s, 16s, 30s (cap).
func sseBackoffFor(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := sseBackoffInitial << (attempt - 1)
	if d <= 0 || d > sseBackoffMax {
		return sseBackoffMax
	}
	return d
}

// New creates a new Bridge.
func New(f forge.Client, d discord.Client, cfg *Config, logger *slog.Logger) *Bridge {
	return &Bridge{
		forge:             f,
		discord:           d,
		cfg:               cfg,
		logger:            logger,
		sessions:          NewSessionMap(),
		dedup:             NewEventDedup(),
		outbox:            NewRetryQueue(logger),
		translators:       make(map[string]*Translator),
		cancelFns:         make(map[string]context.CancelFunc),
		reconnectAttempts: make(map[string]int),
		reconnectLastErr:  make(map[string]string),
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

	if b.cfg.GetChannelConfig(evt.ChannelID) == nil {
		return nil
	}

	// Create Forge session
	metadata := map[string]any{
		"source":            "discord",
		"discord.guildId":   evt.GuildID,
		"discord.channelId": evt.ChannelID,
		"discord.threadId":  evt.ThreadID,
		"discord.userId":    evt.UserID,
		"discord.username":  evt.Username,
	}

	// cwd is empty: the gateway uses its WORKSPACE_DIR, so the bridge image
	// stays portable and never needs to know host paths.
	sessionID, err := b.forge.CreateSession(ctx, "", metadata)
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

	// Successful event delivery resets the SSE reconnect counter for this
	// thread. The relay is healthy.
	b.mu.Lock()
	if _, tracked := b.reconnectAttempts[threadID]; tracked {
		delete(b.reconnectAttempts, threadID)
		delete(b.reconnectLastErr, threadID)
	}
	b.mu.Unlock()

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

	// IMPORTANT: "done" is a TURN terminator, not a SESSION terminator. The
	// runtime loop emits a done event after every assistant turn that has no
	// tool_use (i.e., the agent stopped calling tools and is awaiting the next
	// user message). The SSE stream stays open, the agent stays alive, and the
	// session is reusable for follow-up messages.
	//
	// Treating "done" as session-terminal here was the bug that caused the
	// thread→session mapping to be deleted after the very first phase of
	// every Forge run — silently dropping every subsequent message in the
	// thread. (See incident 2026-06-12: ~9 hours of @Troy mentions routed into
	// the void after spec-phase completion.)
	//
	// Session→thread mappings are now cleaned up only via explicit user
	// actions: the 🛑 reaction (onReactionAdd) or thread archival
	// (onThreadUpdate). Both already call sessions.Delete + cancelRelay.

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
	// Preserve any existing translator state for this thread. We used to
	// blindly construct a new Translator on every (re)connect, which wiped
	// in-flight batching state. On a transient disconnect we want to keep
	// what we have.
	tr, ok := b.translators[threadID]
	if !ok {
		tr = NewTranslator(threadID, "", sessionID,
			b.cfg.ShowThinking, b.cfg.RevealSessionID)
		b.translators[threadID] = tr
	}
	_ = tr // silence unused if struct fields evolve
	b.cancelFns[threadID] = cancel
	b.mu.Unlock()

	go func() {
		defer cancel()

		events, err := b.forge.SubscribeEvents(subCtx, sessionID)
		if err != nil {
			b.logger.Error("failed to subscribe to forge events",
				"session", sessionID, "error", err)
			// Treat subscribe failure as a disconnect so we backoff and retry
			// rather than silently leaking the relay slot.
			b.handleSSEDisconnect(ctx, threadID, sessionID, err)
			return
		}

		for {
			select {
			case evt, ok := <-events:
				if !ok {
					b.handleSSEDisconnect(ctx, threadID, sessionID, nil)
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

func (b *Bridge) handleSSEDisconnect(ctx context.Context, threadID, sessionID string, cause error) {
	// If the thread→session mapping is gone, the relay is intentionally dead
	// (🛑, archive, etc.). Stop reconnecting.
	if b.sessions.GetByThread(threadID) == "" {
		b.mu.Lock()
		delete(b.reconnectAttempts, threadID)
		delete(b.reconnectLastErr, threadID)
		b.mu.Unlock()
		return
	}

	// Increment attempt counter and check the cap.
	b.mu.Lock()
	b.reconnectAttempts[threadID]++
	attempt := b.reconnectAttempts[threadID]
	if cause != nil {
		b.reconnectLastErr[threadID] = cause.Error()
	}
	b.mu.Unlock()

	if attempt > sseMaxReconnectAttempts {
		b.logger.Error("SSE relay declared dead after max reconnect attempts — stopping retries",
			"session", sessionID,
			"thread", threadID,
			"attempts", attempt,
			"last_error", b.reconnectLastErr[threadID])
		// Tear down the relay slot but DO NOT delete the session mapping —
		// the session may still be revivable via /sessions/{id}/messages,
		// and a future bridge restart will pick it up from the pinned
		// forge-meta. We just stop spinning sockets at it.
		b.cancelRelay(threadID)
		b.mu.Lock()
		delete(b.reconnectAttempts, threadID)
		delete(b.reconnectLastErr, threadID)
		b.mu.Unlock()
		// Best-effort warning post to the thread so it isn't a silent death.
		_, _ = b.discord.PostMessage(ctx, threadID,
			"⚠️ Forge event stream lost (max reconnect attempts exceeded). "+
				"This session is no longer being relayed. Restart the bridge or open a new thread to recover.")
		return
	}

	backoff := sseBackoffFor(attempt)
	b.logger.Warn("SSE disconnected, attempting reconnect",
		"session", sessionID,
		"thread", threadID,
		"attempt", attempt,
		"backoff", backoff)

	// Post warning reaction (best effort).
	b.mu.Lock()
	tr, ok := b.translators[threadID]
	b.mu.Unlock()
	if ok && tr.lastBotMsgID != "" {
		_ = b.discord.AddReaction(ctx, threadID, tr.lastBotMsgID, "⚠️")
	}

	select {
	case <-ctx.Done():
		return
	case <-time.After(backoff):
	}

	b.startSSERelay(ctx, threadID, sessionID)
}

func (b *Bridge) cancelRelay(threadID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cancel, ok := b.cancelFns[threadID]; ok {
		cancel()
		delete(b.cancelFns, threadID)
	}
	delete(b.translators, threadID)
	delete(b.reconnectAttempts, threadID)
	delete(b.reconnectLastErr, threadID)
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

// ReconnectingThreads returns a snapshot of threadID→current-attempt-count
// for any thread whose SSE relay is currently in backoff/retry. Used by the
// admin /metrics endpoint to surface relay health.
func (b *Bridge) ReconnectingThreads() map[string]int {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]int, len(b.reconnectAttempts))
	for k, v := range b.reconnectAttempts {
		out[k] = v
	}
	return out
}
