package bus

import (
	"sync"
	"testing"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSetGetSession(t *testing.T) {
	r := require.New(t)

	// Store a session
	meta := &types.SessionMeta{
		SessionID:    "troy-session",
		CWD:          "/tmp/greendale",
		Metadata:     map[string]any{"class": "biology"},
		CreatedAt:    time.Now().UnixMilli(),
		LastActiveAt: time.Now().UnixMilli(),
	}
	SetSession(meta)

	// Retrieve it
	got := GetSession("troy-session")
	r.NotNil(got)
	r.Equal("troy-session", got.SessionID)
	r.Equal("/tmp/greendale", got.CWD)
	r.Equal("biology", got.Metadata["class"])

	// Returns a copy — modifying it doesn't affect stored value
	got.CWD = "/tmp/modified"
	original := GetSession("troy-session")
	r.Equal("/tmp/greendale", original.CWD)
}

func TestGetSession_NotFound(t *testing.T) {
	r := require.New(t)
	r.Nil(GetSession("nonexistent-session-id"))
}

func TestPublishEvent_Subscribe(t *testing.T) {
	r := require.New(t)

	events, unsub := Subscribe("pub-sub-test")
	defer unsub()

	event := types.OutboundEvent{
		ID:        "evt-1",
		SessionID: "pub-sub-test",
		Type:      "text",
		Content:   "cool cool cool",
		Timestamp: time.Now().UnixMilli(),
	}
	PublishEvent("pub-sub-test", event)

	select {
	case received := <-events:
		r.Equal("evt-1", received.ID)
		r.Equal("cool cool cool", received.Content)
	case <-time.After(time.Second):
		r.Fail("timed out waiting for event")
	}
}

func TestPublishEvent_MultipleSubscribers(t *testing.T) {
	r := require.New(t)

	ch1, unsub1 := Subscribe("multi-sub")
	defer unsub1()
	ch2, unsub2 := Subscribe("multi-sub")
	defer unsub2()

	PublishEvent("multi-sub", types.OutboundEvent{
		ID:   "evt-multi",
		Type: "text",
	})

	for _, ch := range []<-chan types.OutboundEvent{ch1, ch2} {
		select {
		case evt := <-ch:
			r.Equal("evt-multi", evt.ID)
		case <-time.After(time.Second):
			r.Fail("timed out")
		}
	}
}

func TestPublishEvent_DropsForSlowSubscriber(t *testing.T) {
	r := require.New(t)

	events, unsub := Subscribe("slow-sub")
	defer unsub()

	// Fill the channel (capacity 64)
	for i := 0; i < 70; i++ {
		PublishEvent("slow-sub", types.OutboundEvent{
			ID:   "overflow",
			Type: "text",
		})
	}

	// Should have exactly 64 events (channel capacity)
	count := 0
	for {
		select {
		case <-events:
			count++
		default:
			goto done
		}
	}
done:
	r.Equal(64, count, "should drop events beyond channel capacity")
}

func TestUnsubscribe_ClosesChannel(t *testing.T) {
	r := require.New(t)

	events, unsub := Subscribe("unsub-test")
	unsub()

	// Channel should be closed
	_, open := <-events
	r.False(open, "channel should be closed after unsub")
}

func TestPushPullMessage(t *testing.T) {
	r := require.New(t)

	msg := types.InboundMessage{
		SessionID: "push-pull",
		Text:      "hello from troy",
		User:      "troy",
		Source:    "api",
		Timestamp: time.Now().UnixMilli(),
	}
	PushMessage("push-pull", msg)

	// PullMessage in a goroutine (it blocks)
	var got types.InboundMessage
	done := make(chan struct{})
	go func() {
		got = PullMessage("push-pull")
		close(done)
	}()

	select {
	case <-done:
		r.Equal("hello from troy", got.Text)
		r.Equal("troy", got.User)
	case <-time.After(time.Second):
		r.Fail("PullMessage did not return")
	}
}

func TestPullMessage_BlocksUntilPush(t *testing.T) {
	r := require.New(t)

	var got types.InboundMessage
	done := make(chan struct{})

	go func() {
		got = PullMessage("blocking-test")
		close(done)
	}()

	// Ensure it's actually blocking
	time.Sleep(50 * time.Millisecond)
	select {
	case <-done:
		r.Fail("PullMessage should block until a message is pushed")
	default:
		// good — it's still blocking
	}

	// Now push
	PushMessage("blocking-test", types.InboundMessage{
		SessionID: "blocking-test",
		Text:      "delayed message",
	})

	select {
	case <-done:
		r.Equal("delayed message", got.Text)
	case <-time.After(time.Second):
		r.Fail("PullMessage did not unblock")
	}
}

func TestConcurrentAccess(t *testing.T) {
	r := require.New(t)

	// Stress test concurrent operations
	var wg sync.WaitGroup
	sessionID := "concurrent-test"

	// Set session from multiple goroutines
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			SetSession(&types.SessionMeta{
				SessionID: sessionID,
				Metadata:  map[string]any{},
			})
		}()
	}

	// Get session concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = GetSession(sessionID)
		}()
	}

	// Subscribe/publish concurrently
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, unsub := Subscribe(sessionID)
			defer unsub()
			select {
			case <-ch:
			case <-time.After(100 * time.Millisecond):
			}
		}()
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			PublishEvent(sessionID, types.OutboundEvent{ID: "stress", Type: "text"})
		}()
	}

	wg.Wait()
	r.NotNil(GetSession(sessionID))
}
