package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jelmersnoeck/forge/internal/types"
)

// waitForWaiter sets up an OnWaiterReady hook and returns a function that
// blocks until a waiter is registered. Call before launching the goroutine.
func waitForWaiter(hub *Hub) func() {
	ready := make(chan struct{}, 1)
	hub.OnWaiterReady(func() { ready <- struct{}{} })
	return func() { <-ready }
}

func TestHub_PushPull_Buffered(t *testing.T) {
	// Push first, then pull — message should be buffered.
	hub := NewHub()

	hub.PushMessage(types.InboundMessage{
		Text: "Cool. Cool cool cool.",
		User: "Abed Nadir",
	})

	msg, ok := hub.PullMessage(context.Background())
	require.True(t, ok)
	require.Equal(t, "Cool. Cool cool cool.", msg.Text)
	require.Equal(t, "Abed Nadir", msg.User)
}

func TestHub_PullPush_Waiter(t *testing.T) {
	// Pull blocks until push delivers via waiter channel.
	hub := NewHub()
	awaitReady := waitForWaiter(hub)

	var got types.InboundMessage
	done := make(chan struct{})
	go func() {
		got, _ = hub.PullMessage(context.Background())
		close(done)
	}()

	awaitReady()

	hub.PushMessage(types.InboundMessage{
		Text: "Troy and Abed in the morning!",
		User: "Troy Barnes",
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("PullMessage did not return in time")
	}

	require.Equal(t, "Troy and Abed in the morning!", got.Text)
	require.Equal(t, "Troy Barnes", got.User)
}

func TestHub_FIFO(t *testing.T) {
	hub := NewHub()

	hub.PushMessage(types.InboundMessage{Text: "First: Pop Pop!", User: "Magnitude"})
	hub.PushMessage(types.InboundMessage{Text: "Second: Streets ahead", User: "Pierce Hawthorne"})

	first, _ := hub.PullMessage(context.Background())
	second, _ := hub.PullMessage(context.Background())

	require.Equal(t, "First: Pop Pop!", first.Text)
	require.Equal(t, "Second: Streets ahead", second.Text)
}

func TestHub_SubscribePublish(t *testing.T) {
	hub := NewHub()

	events, unsub := hub.Subscribe()
	defer unsub()

	hub.PublishEvent(types.OutboundEvent{
		ID:        "evt-1",
		SessionID: "greendale-101",
		Type:      "text",
		Content:   "E Pluribus Anus",
	})

	select {
	case ev := <-events:
		require.Equal(t, "evt-1", ev.ID)
		require.Equal(t, "greendale-101", ev.SessionID)
		require.Equal(t, "text", ev.Type)
		require.Equal(t, "E Pluribus Anus", ev.Content)
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive event in time")
	}
}

func TestHub_MultipleSubscribers(t *testing.T) {
	hub := NewHub()

	events1, unsub1 := hub.Subscribe()
	defer unsub1()
	events2, unsub2 := hub.Subscribe()
	defer unsub2()

	hub.PublishEvent(types.OutboundEvent{
		ID:      "evt-2",
		Type:    "text",
		Content: "Six seasons and a movie!",
	})

	for _, ch := range []<-chan types.OutboundEvent{events1, events2} {
		select {
		case ev := <-ch:
			require.Equal(t, "evt-2", ev.ID)
			require.Equal(t, "Six seasons and a movie!", ev.Content)
		case <-time.After(2 * time.Second):
			t.Fatal("subscriber did not receive event")
		}
	}
}

func TestHub_Unsubscribe(t *testing.T) {
	hub := NewHub()

	events, unsub := hub.Subscribe()
	unsub()

	// Channel should be closed after unsubscribe.
	_, ok := <-events
	require.False(t, ok, "channel should be closed after unsubscribe")
}

func TestHub_ConcurrentPushPull(t *testing.T) {
	hub := NewHub()
	const n = 50

	var wg sync.WaitGroup
	received := make([]types.InboundMessage, n)

	// Track waiter registrations with a buffered channel.
	allReady := make(chan struct{}, n)
	hub.OnWaiterReady(func() { allReady <- struct{}{} })

	// Start n pullers
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			received[idx], _ = hub.PullMessage(context.Background())
		}(i)
	}

	// Wait for all pullers to register as waiters.
	for i := 0; i < n; i++ {
		<-allReady
	}

	// Push n messages
	for i := 0; i < n; i++ {
		hub.PushMessage(types.InboundMessage{
			Text: "Enrollment at Greendale Community College",
			User: "Dean Pelton",
		})
	}

	wg.Wait()

	for _, msg := range received {
		require.Equal(t, "Dean Pelton", msg.User)
	}
}

func TestHub_DrainInterrupt(t *testing.T) {
	tests := map[string]struct {
		setup func(*Hub)
		want  bool // true = channel was drained (had pending signal)
	}{
		"no pending signal": {
			setup: func(h *Hub) {},
			want:  false,
		},
		"stale signal drained": {
			setup: func(h *Hub) {
				h.TriggerInterrupt()
			},
			want: true,
		},
		"drain is idempotent": {
			setup: func(h *Hub) {
				h.TriggerInterrupt()
				h.DrainInterrupt() // first drain clears it
			},
			want: false, // second drain finds nothing
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			hub := NewHub()
			tc.setup(hub)

			got := hub.DrainInterrupt()
			r.Equal(tc.want, got, "DrainInterrupt return value")

			// After drain, channel must be empty.
			select {
			case <-hub.InterruptChannel():
				r.Fail("interrupt channel should be empty after drain")
			default:
				// good — nothing pending
			}
		})
	}
}

func TestHub_DrainInterrupt_RepeatedCallsOnEmpty(t *testing.T) {
	// Calling DrainInterrupt multiple times on an empty channel should
	// be safe and always return false with no side effects.
	r := require.New(t)
	hub := NewHub()

	for i := 0; i < 10; i++ {
		r.False(hub.DrainInterrupt(), "drain #%d on empty channel should return false", i)
	}

	// Channel should still accept a new interrupt after repeated drains.
	hub.TriggerInterrupt()
	r.True(hub.DrainInterrupt(), "drain after trigger should return true")
}

func TestHub_DrainInterrupt_DoesNotBlockActiveTurn(t *testing.T) {
	// Drain should not interfere with an interrupt arriving during
	// an active turn (interrupt goroutine consumes it, not drain).
	r := require.New(t)
	hub := NewHub()

	// Simulate: drain at turn start (nothing pending), then trigger
	// interrupt mid-turn — it should still be receivable.
	hub.DrainInterrupt()
	hub.TriggerInterrupt()

	select {
	case <-hub.InterruptChannel():
		// good — interrupt arrived normally
	case <-time.After(100 * time.Millisecond):
		r.Fail("interrupt should be receivable after drain when sent mid-turn")
	}
}

func TestHub_PushMessage_ReturnsImmediateStatus(t *testing.T) {
	tests := map[string]struct {
		setup    func(*Hub) // Setup function to configure hub state
		expected bool       // Expected return value from PushMessage
	}{
		"returns true when worker is idle (waiter exists)": {
			setup: func(hub *Hub) {
				awaitReady := waitForWaiter(hub)
				go func() {
					hub.PullMessage(context.Background())
				}()
				awaitReady()
			},
			expected: true,
		},
		"returns false when worker is busy (no waiter)": {
			setup: func(hub *Hub) {
				// Don't set up any waiter - queue is empty
			},
			expected: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			hub := NewHub()
			tc.setup(hub)

			immediate := hub.PushMessage(types.InboundMessage{
				Text: "Test message",
				User: "Troy Barnes",
			})

			require.Equal(t, tc.expected, immediate)
		})
	}
}

func TestHub_PullMessage_ContextCancelled(t *testing.T) {
	r := require.New(t)
	hub := NewHub()

	ctx, cancel := context.WithCancel(context.Background())
	awaitReady := waitForWaiter(hub)

	done := make(chan struct{})
	var msg types.InboundMessage
	var ok bool
	go func() {
		defer close(done)
		msg, ok = hub.PullMessage(ctx)
	}()

	awaitReady()

	// Cancel the context — PullMessage should return.
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("PullMessage did not return after context cancellation")
	}

	r.False(ok, "PullMessage should return false when context is cancelled")
	r.Empty(msg.Text)

	// The waiter should have been cleaned up — no leaks.
	hub.qmu.Lock()
	r.Empty(hub.waiters, "waiters should be empty after cancellation")
	hub.qmu.Unlock()
}

func TestHub_PullMessage_ContextCancelled_MessageNotLost(t *testing.T) {
	// If a message arrives concurrently with cancellation, it must not be lost.
	r := require.New(t)
	hub := NewHub()

	ctx, cancel := context.WithCancel(context.Background())
	awaitReady := waitForWaiter(hub)

	done := make(chan struct{})
	go func() {
		defer close(done)
		hub.PullMessage(ctx)
	}()

	awaitReady()

	// Push a message and immediately cancel — message may arrive before or after.
	hub.PushMessage(types.InboundMessage{Text: "Don't lose me", User: "Britta"})
	cancel()

	<-done

	// The message was either delivered to the PullMessage call or re-queued.
	// Either way, it should be retrievable.
	hub.qmu.Lock()
	queued := len(hub.queue)
	hub.qmu.Unlock()

	// If it was re-queued, pull it. If PullMessage got it, queue is empty — both are fine.
	if queued > 0 {
		msg, ok := hub.PullMessage(context.Background())
		r.True(ok)
		r.Equal("Don't lose me", msg.Text)
	}
}

func TestHub_PeekSteeringMessage(t *testing.T) {
	tests := map[string]struct {
		setup    func(*Hub)
		wantText string
		wantOK   bool
	}{
		"empty queue": {
			setup:  func(h *Hub) {},
			wantOK: false,
		},
		"returns first queued message without consuming": {
			setup: func(h *Hub) {
				h.PushMessage(types.InboundMessage{Text: "Streets ahead", User: "Pierce Hawthorne"})
				h.PushMessage(types.InboundMessage{Text: "Pop Pop!", User: "Magnitude"})
			},
			wantText: "Streets ahead",
			wantOK:   true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			hub := NewHub()
			tc.setup(hub)

			text, ok := hub.PeekSteeringMessage()
			r.Equal(tc.wantOK, ok)
			r.Equal(tc.wantText, text)

			// Peek again — should return the same result (non-destructive).
			if tc.wantOK {
				text2, ok2 := hub.PeekSteeringMessage()
				r.True(ok2)
				r.Equal(tc.wantText, text2)
			}
		})
	}
}

func TestHub_ConsumeSteeringMessage(t *testing.T) {
	tests := map[string]struct {
		setup    func(*Hub)
		wantText string
		wantOK   bool
	}{
		"empty queue": {
			setup:  func(h *Hub) {},
			wantOK: false,
		},
		"consumes first message": {
			setup: func(h *Hub) {
				h.PushMessage(types.InboundMessage{Text: "Cool cool cool", User: "Abed Nadir"})
				h.PushMessage(types.InboundMessage{Text: "That's the opposite of Batman", User: "Abed Nadir"})
			},
			wantText: "Cool cool cool",
			wantOK:   true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			hub := NewHub()
			tc.setup(hub)

			text, ok := hub.ConsumeSteeringMessage()
			r.Equal(tc.wantOK, ok)
			r.Equal(tc.wantText, text)

			// Second consume should get the next message or empty.
			if tc.wantOK {
				text2, ok2 := hub.ConsumeSteeringMessage()
				r.True(ok2)
				r.Equal("That's the opposite of Batman", text2)

				// Third consume — queue exhausted.
				_, ok3 := hub.ConsumeSteeringMessage()
				r.False(ok3)
			}
		})
	}
}
