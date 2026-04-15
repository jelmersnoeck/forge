// Package agent implements a single-session agent process with its own
// HTTP server, conversation loop, and tool execution.
package agent

import (
	"log"
	"sync"

	"github.com/jelmersnoeck/forge/internal/types"
)

// Hub is a single-session message queue and event pub/sub.
// It uses a waiter-list pattern: if a worker is already waiting when a
// message arrives, its channel is resolved directly — no polling, no timers.
type Hub struct {
	queue   []types.InboundMessage
	waiters []chan types.InboundMessage
	qmu     sync.Mutex

	subs []chan types.OutboundEvent
	smu  sync.RWMutex

	// Queue management for deferred task execution
	immediateQueue  []string // bash commands to run after each tool
	completionQueue []string // bash commands to run on completion
	queueMu         sync.Mutex

	// Interrupt management
	interruptCh chan struct{}
	interruptMu sync.Mutex

	// Review trigger
	reviewCh chan string // base branch name (empty = auto-detect)
	reviewMu sync.Mutex
}

// NewHub creates a new Hub.
func NewHub() *Hub {
	return &Hub{
		interruptCh: make(chan struct{}, 1),
		reviewCh:    make(chan string, 1),
	}
}

// PushMessage enqueues a message for the worker. If the worker is already
// blocking on PullMessage, the message is delivered directly via the waiter
// channel. Returns true if the message was delivered immediately (worker idle),
// false if it was queued (worker busy).
func (h *Hub) PushMessage(msg types.InboundMessage) bool {
	h.qmu.Lock()
	if len(h.waiters) > 0 {
		ch := h.waiters[0]
		h.waiters = h.waiters[1:]
		h.qmu.Unlock()
		ch <- msg
		return true
	}
	h.queue = append(h.queue, msg)
	h.qmu.Unlock()
	return false
}

// PullMessage blocks until a message is available.
func (h *Hub) PullMessage() types.InboundMessage {
	h.qmu.Lock()
	if len(h.queue) > 0 {
		msg := h.queue[0]
		h.queue = h.queue[1:]
		h.qmu.Unlock()
		return msg
	}

	ch := make(chan types.InboundMessage, 1)
	h.waiters = append(h.waiters, ch)
	h.qmu.Unlock()

	return <-ch
}

// PublishEvent sends an event to all subscribers.
func (h *Hub) PublishEvent(event types.OutboundEvent) {
	h.smu.RLock()
	defer h.smu.RUnlock()
	for _, ch := range h.subs {
		select {
		case ch <- event:
		default:
			// subscriber too slow, drop event
		}
	}
}

// Subscribe returns a channel of events and an unsubscribe function.
func (h *Hub) Subscribe() (<-chan types.OutboundEvent, func()) {
	ch := make(chan types.OutboundEvent, 64)

	h.smu.Lock()
	h.subs = append(h.subs, ch)
	h.smu.Unlock()

	unsub := func() {
		h.smu.Lock()
		defer h.smu.Unlock()
		for i, c := range h.subs {
			if c == ch {
				h.subs = append(h.subs[:i], h.subs[i+1:]...)
				break
			}
		}
		close(ch)
	}

	return ch, unsub
}

// EnqueueImmediate adds a command to run after each tool execution.
// Pass empty string to clear the queue.
func (h *Hub) EnqueueImmediate(command string) {
	h.queueMu.Lock()
	defer h.queueMu.Unlock()
	if command == "" {
		h.immediateQueue = nil
	} else {
		h.immediateQueue = append(h.immediateQueue, command)
	}
}

// EnqueueCompletion adds a command to run when work completes.
func (h *Hub) EnqueueCompletion(command string) {
	h.queueMu.Lock()
	defer h.queueMu.Unlock()
	h.completionQueue = append(h.completionQueue, command)
}

// GetImmediateQueue returns a copy of the immediate queue.
func (h *Hub) GetImmediateQueue() []string {
	h.queueMu.Lock()
	defer h.queueMu.Unlock()
	queue := make([]string, len(h.immediateQueue))
	copy(queue, h.immediateQueue)
	return queue
}

// PullCompletionQueue returns and clears the completion queue.
func (h *Hub) PullCompletionQueue() []string {
	h.queueMu.Lock()
	defer h.queueMu.Unlock()
	queue := h.completionQueue
	h.completionQueue = nil
	return queue
}

// TriggerInterrupt signals that the current work should be interrupted.
func (h *Hub) TriggerInterrupt() {
	h.interruptMu.Lock()
	defer h.interruptMu.Unlock()
	select {
	case h.interruptCh <- struct{}{}:
	default:
		// Already pending, skip
	}
}

// InterruptChannel returns a channel that receives interrupt signals.
func (h *Hub) InterruptChannel() <-chan struct{} {
	return h.interruptCh
}

// DrainInterrupt discards any pending interrupt signal. Called at the
// start of each turn to prevent a stale Ctrl+C from the previous turn
// from immediately cancelling the new one. Returns true if a stale
// signal was actually drained.
func (h *Hub) DrainInterrupt() bool {
	select {
	case <-h.interruptCh:
		log.Println("[hub] drained stale interrupt signal")
		return true
	default:
		return false
	}
}

// TriggerReview signals the worker to start a code review.
// baseBranch is the branch to diff against (empty = auto-detect).
func (h *Hub) TriggerReview(baseBranch string) {
	h.reviewMu.Lock()
	defer h.reviewMu.Unlock()
	select {
	case h.reviewCh <- baseBranch:
	default:
		// Already pending, skip
	}
}

// ReviewChannel returns a channel that receives review requests (base branch name).
func (h *Hub) ReviewChannel() <-chan string {
	return h.reviewCh
}

// IsIdle reports whether the worker is waiting for a message (i.e., no active
// conversation turn is in progress). The monitor uses this to avoid touching
// the git repo while the agent is working.
func (h *Hub) IsIdle() bool {
	h.qmu.Lock()
	defer h.qmu.Unlock()
	return len(h.waiters) > 0
}
