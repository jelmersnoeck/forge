package bridge

import "sync"

const dedupBufferSize = 256

// EventDedup holds a ring buffer of recently-seen event IDs per session.
type EventDedup struct {
	mu      sync.Mutex
	buffers map[string]*ringBuffer
}

// NewEventDedup creates a new dedup tracker.
func NewEventDedup() *EventDedup {
	return &EventDedup{
		buffers: make(map[string]*ringBuffer),
	}
}

// Seen returns true if the event ID was already recorded for the session.
func (d *EventDedup) Seen(sessionID, eventID string) bool {
	if eventID == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	buf, ok := d.buffers[sessionID]
	if !ok {
		return false
	}
	return buf.Contains(eventID)
}

// Record marks an event ID as seen for the session.
func (d *EventDedup) Record(sessionID, eventID string) {
	if eventID == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	buf, ok := d.buffers[sessionID]
	if !ok {
		buf = newRingBuffer(dedupBufferSize)
		d.buffers[sessionID] = buf
	}
	buf.Add(eventID)
}

// Drop removes the dedup buffer for a session.
func (d *EventDedup) Drop(sessionID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.buffers, sessionID)
}

// ringBuffer is a fixed-size circular buffer of strings.
type ringBuffer struct {
	items []string
	pos   int
	size  int
	full  bool
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{
		items: make([]string, size),
		size:  size,
	}
}

func (r *ringBuffer) Add(s string) {
	r.items[r.pos] = s
	r.pos = (r.pos + 1) % r.size
	if r.pos == 0 {
		r.full = true
	}
}

func (r *ringBuffer) Contains(s string) bool {
	limit := r.size
	if !r.full {
		limit = r.pos
	}
	for i := 0; i < limit; i++ {
		if r.items[i] == s {
			return true
		}
	}
	return false
}
