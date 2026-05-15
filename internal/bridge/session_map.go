package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sync"

	"github.com/jelmersnoeck/forge/internal/discord"
)

// forgeMetaRe matches the session ID inside a ```forge-meta fenced block.
var forgeMetaRe = regexp.MustCompile("(?s)```forge-meta\\s*\n\\s*session:\\s*([^\\s`]+)\\s*\n\\s*```")

// ParseForgeMetaSession extracts the session ID from a forge-meta fenced block.
// Returns empty string if not found.
func ParseForgeMetaSession(content string) string {
	m := forgeMetaRe.FindStringSubmatch(content)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// ForgeMetaMessage returns the content for a pinned forge-meta message.
func ForgeMetaMessage(sessionID string) string {
	return fmt.Sprintf("```forge-meta\nsession: %s\n```", sessionID)
}

// SessionMap maps Discord thread IDs to Forge session IDs (and reverse).
// Thread-safe for concurrent access.
type SessionMap struct {
	mu        sync.RWMutex
	byThread  map[string]string // threadID → sessionID
	bySession map[string]string // sessionID → threadID
}

// NewSessionMap creates an empty SessionMap.
func NewSessionMap() *SessionMap {
	return &SessionMap{
		byThread:  make(map[string]string),
		bySession: make(map[string]string),
	}
}

// Set adds or updates a thread↔session mapping.
func (m *SessionMap) Set(threadID, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byThread[threadID] = sessionID
	m.bySession[sessionID] = threadID
}

// GetByThread returns the sessionID for a thread, or empty string.
func (m *SessionMap) GetByThread(threadID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byThread[threadID]
}

// GetBySession returns the threadID for a session, or empty string.
func (m *SessionMap) GetBySession(sessionID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.bySession[sessionID]
}

// Delete removes a mapping by threadID.
func (m *SessionMap) Delete(threadID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sid, ok := m.byThread[threadID]; ok {
		delete(m.bySession, sid)
	}
	delete(m.byThread, threadID)
}

// Len returns the number of active mappings.
func (m *SessionMap) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.byThread)
}

// ThreadIDs returns all mapped thread IDs.
func (m *SessionMap) ThreadIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.byThread))
	for tid := range m.byThread {
		out = append(out, tid)
	}
	return out
}

// Entries returns all mappings as threadID→sessionID pairs.
func (m *SessionMap) Entries() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]string, len(m.byThread))
	for k, v := range m.byThread {
		out[k] = v
	}
	return out
}

// Rebuild scans configured channels, reads pinned messages, and rebuilds
// the in-memory thread→session mapping from forge-meta blocks.
func (m *SessionMap) Rebuild(ctx context.Context, dc discord.Client, channelIDs []string, logger *slog.Logger) error {
	m.mu.Lock()
	m.byThread = make(map[string]string)
	m.bySession = make(map[string]string)
	m.mu.Unlock()

	for _, chID := range channelIDs {
		threads, err := dc.ListActiveThreads(ctx, chID)
		if err != nil {
			logger.Error("failed to list threads for channel", "channel", chID, "error", err)
			continue
		}

		for _, t := range threads {
			if t.Archived {
				continue
			}

			pins, err := dc.GetPinnedMessages(ctx, t.ID)
			if err != nil {
				logger.Error("failed to get pinned messages", "thread", t.ID, "error", err)
				continue
			}

			for _, pin := range pins {
				sessionID := ParseForgeMetaSession(pin.Content)
				if sessionID != "" {
					m.Set(t.ID, sessionID)
					logger.Info("rebuilt mapping", "thread", t.ID, "session", sessionID)
					break // one session per thread
				}
			}
		}
	}

	return nil
}
