package agent

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jelmersnoeck/forge/internal/types"
)

func TestHub_PushPull_Buffered(t *testing.T) {
	// Push first, then pull — message should be buffered.
	hub := NewHub()

	hub.PushMessage(types.InboundMessage{
		Text: "Cool. Cool cool cool.",
		User: "Abed Nadir",
	})

	msg := hub.PullMessage()
	require.Equal(t, "Cool. Cool cool cool.", msg.Text)
	require.Equal(t, "Abed Nadir", msg.User)
}

func TestHub_PullPush_Waiter(t *testing.T) {
	// Pull blocks until push delivers via waiter channel.
	hub := NewHub()

	var got types.InboundMessage
	done := make(chan struct{})
	go func() {
		got = hub.PullMessage()
		close(done)
	}()

	// Give the goroutine time to register as a waiter.
	time.Sleep(20 * time.Millisecond)

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

	first := hub.PullMessage()
	second := hub.PullMessage()

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

	// Start n pullers
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			received[idx] = hub.PullMessage()
		}(i)
	}

	// Give pullers time to register as waiters.
	time.Sleep(30 * time.Millisecond)

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
				// Start a goroutine that will wait for a message
				go func() {
					hub.PullMessage()
				}()
				// Give it time to register as a waiter
				time.Sleep(20 * time.Millisecond)
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
