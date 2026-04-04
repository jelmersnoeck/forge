// Package bus provides an in-memory event pub/sub and session metadata store
// for communication between the gateway and agents.
//
//   publishEvent → agent relay emits output
//   subscribe    → SSE handler receives output
package bus

import (
	"sync"

	"github.com/jelmersnoeck/forge/internal/types"
)

var (
	sessions = struct {
		sync.RWMutex
		m map[string]*types.SessionMeta
	}{m: make(map[string]*types.SessionMeta)}

	subs = struct {
		sync.RWMutex
		m map[string][]chan types.OutboundEvent
	}{m: make(map[string][]chan types.OutboundEvent)}
)

// GetSession returns session metadata, or nil if not found.
func GetSession(sessionID string) *types.SessionMeta {
	sessions.RLock()
	defer sessions.RUnlock()
	meta, ok := sessions.m[sessionID]
	if !ok {
		return nil
	}
	cp := *meta
	return &cp
}

// SetSession stores session metadata.
func SetSession(meta *types.SessionMeta) {
	sessions.Lock()
	defer sessions.Unlock()
	sessions.m[meta.SessionID] = meta
}

// PublishEvent sends an event to all subscribers for a session.
func PublishEvent(sessionID string, event types.OutboundEvent) {
	subs.RLock()
	defer subs.RUnlock()
	for _, ch := range subs.m[sessionID] {
		select {
		case ch <- event:
		default:
			// subscriber too slow, drop event
		}
	}
}

// Subscribe returns a channel of events for a session and an unsubscribe function.
func Subscribe(sessionID string) (<-chan types.OutboundEvent, func()) {
	ch := make(chan types.OutboundEvent, 64)

	subs.Lock()
	subs.m[sessionID] = append(subs.m[sessionID], ch)
	subs.Unlock()

	unsub := func() {
		subs.Lock()
		defer subs.Unlock()
		list := subs.m[sessionID]
		for i, c := range list {
			if c == ch {
				subs.m[sessionID] = append(list[:i], list[i+1:]...)
				break
			}
		}
		close(ch)
	}

	return ch, unsub
}
