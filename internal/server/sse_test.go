package server

import (
	"sync"
	"testing"
	"time"
)

func TestSSEBroker_SubscribePublish(t *testing.T) {
	b := NewSSEBroker()

	ch := b.Subscribe("job-1")
	defer b.Unsubscribe("job-1", ch)

	b.Publish("job-1", Event{Type: "test", Data: "hello"})

	select {
	case event := <-ch:
		if event.Type != "test" {
			t.Errorf("expected event type %q, got %q", "test", event.Type)
		}
		if event.Data != "hello" {
			t.Errorf("expected event data %q, got %v", "hello", event.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestSSEBroker_MultipleSubscribers(t *testing.T) {
	b := NewSSEBroker()

	ch1 := b.Subscribe("job-1")
	ch2 := b.Subscribe("job-1")
	defer b.Unsubscribe("job-1", ch1)
	defer b.Unsubscribe("job-1", ch2)

	b.Publish("job-1", Event{Type: "test", Data: "broadcast"})

	for i, ch := range []chan Event{ch1, ch2} {
		select {
		case event := <-ch:
			if event.Type != "test" {
				t.Errorf("subscriber %d: expected type %q, got %q", i, "test", event.Type)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timed out", i)
		}
	}
}

func TestSSEBroker_PublishToWrongJob(t *testing.T) {
	b := NewSSEBroker()

	ch := b.Subscribe("job-1")
	defer b.Unsubscribe("job-1", ch)

	// Publish to a different job should not deliver.
	b.Publish("job-2", Event{Type: "test", Data: "wrong"})

	select {
	case <-ch:
		t.Fatal("received event from wrong job")
	case <-time.After(50 * time.Millisecond):
		// Expected — no event received.
	}
}

func TestSSEBroker_Unsubscribe(t *testing.T) {
	b := NewSSEBroker()

	ch := b.Subscribe("job-1")
	b.Unsubscribe("job-1", ch)

	// Channel should be closed after unsubscribe.
	_, ok := <-ch
	if ok {
		t.Error("expected channel to be closed after unsubscribe")
	}
}

func TestSSEBroker_Cleanup(t *testing.T) {
	b := NewSSEBroker()

	ch1 := b.Subscribe("job-1")
	ch2 := b.Subscribe("job-1")

	b.Cleanup("job-1")

	// Both channels should be closed.
	if _, ok := <-ch1; ok {
		t.Error("expected ch1 to be closed after cleanup")
	}
	if _, ok := <-ch2; ok {
		t.Error("expected ch2 to be closed after cleanup")
	}
}

func TestSSEBroker_ConcurrentPublish(t *testing.T) {
	b := NewSSEBroker()

	ch := b.Subscribe("job-1")
	defer b.Unsubscribe("job-1", ch)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			b.Publish("job-1", Event{Type: "test", Data: n})
		}(i)
	}
	wg.Wait()

	// Drain channel and count.
	count := 0
	for {
		select {
		case <-ch:
			count++
		default:
			goto done
		}
	}
done:
	if count == 0 {
		t.Error("expected to receive at least some events")
	}
}

func TestFormatSSEEvent(t *testing.T) {
	event := Event{
		Type: "job_started",
		Data: map[string]string{"job_id": "abc"},
	}
	raw := formatSSEEvent(event)
	s := string(raw)

	if s == "" {
		t.Fatal("expected non-empty SSE frame")
	}
	// Check SSE format.
	if !contains(s, "event: job_started") {
		t.Errorf("expected 'event: job_started' in %q", s)
	}
	if !contains(s, "data: ") {
		t.Errorf("expected 'data: ' in %q", s)
	}
	if !contains(s, `"job_id"`) {
		t.Errorf("expected job_id in data in %q", s)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
