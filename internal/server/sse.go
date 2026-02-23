package server

import (
	"encoding/json"
	"sync"
)

// Event is an SSE event sent to connected clients.
type Event struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// SSEBroker manages per-job SSE subscriber channels.
type SSEBroker struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan Event]struct{}
}

// NewSSEBroker creates a new SSE broker.
func NewSSEBroker() *SSEBroker {
	return &SSEBroker{
		subscribers: make(map[string]map[chan Event]struct{}),
	}
}

// Subscribe creates a new channel that receives events for the given job ID.
// The caller must call Unsubscribe when done to avoid leaking goroutines.
func (b *SSEBroker) Subscribe(jobID string) chan Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan Event, 64)
	if b.subscribers[jobID] == nil {
		b.subscribers[jobID] = make(map[chan Event]struct{})
	}
	b.subscribers[jobID][ch] = struct{}{}
	return ch
}

// Unsubscribe removes a subscriber channel for a job and closes it.
func (b *SSEBroker) Unsubscribe(jobID string, ch chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if subs, ok := b.subscribers[jobID]; ok {
		delete(subs, ch)
		close(ch)
		if len(subs) == 0 {
			delete(b.subscribers, jobID)
		}
	}
}

// Publish sends an event to all subscribers of a job.
func (b *SSEBroker) Publish(jobID string, event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	subs, ok := b.subscribers[jobID]
	if !ok {
		return
	}
	for ch := range subs {
		// Non-blocking send; drop the event if the subscriber is slow.
		select {
		case ch <- event:
		default:
		}
	}
}

// Cleanup removes all subscribers for a job.
func (b *SSEBroker) Cleanup(jobID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if subs, ok := b.subscribers[jobID]; ok {
		for ch := range subs {
			close(ch)
		}
		delete(b.subscribers, jobID)
	}
}

// formatSSEEvent formats an Event as an SSE text frame.
func formatSSEEvent(event Event) []byte {
	data, err := json.Marshal(event.Data)
	if err != nil {
		data = []byte(`{"error":"marshal failed"}`)
	}
	// SSE format: "event: <type>\ndata: <json>\n\n"
	buf := make([]byte, 0, len(event.Type)+len(data)+32)
	buf = append(buf, "event: "...)
	buf = append(buf, event.Type...)
	buf = append(buf, '\n')
	buf = append(buf, "data: "...)
	buf = append(buf, data...)
	buf = append(buf, '\n', '\n')
	return buf
}
