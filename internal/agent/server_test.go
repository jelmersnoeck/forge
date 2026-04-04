package agent

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jelmersnoeck/forge/internal/types"
)

func newTestServer(hub *Hub, sessionID string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth(sessionID))
	mux.HandleFunc("POST /messages", handleMessages(hub, sessionID))
	mux.HandleFunc("GET /events", handleSSE(hub))
	return httptest.NewServer(mux)
}

func TestHealthEndpoint(t *testing.T) {
	hub := NewHub()
	srv := newTestServer(hub, "greendale-101")
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)
	require.Equal(t, "ok", body["status"])
	require.Equal(t, "greendale-101", body["sessionId"])
}

func TestPostMessages_Accepted(t *testing.T) {
	hub := NewHub()
	srv := newTestServer(hub, "study-group-7")
	defer srv.Close()

	payload := `{"text":"Have you ever heard of the Darkest Timeline?","user":"Abed Nadir","source":"dreamatorium"}`
	resp, err := http.Post(srv.URL+"/messages", "application/json", strings.NewReader(payload))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	var body map[string]string
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)
	require.Equal(t, "queued", body["status"])

	// The message should be in the hub queue.
	msg := hub.PullMessage()
	require.Equal(t, "Have you ever heard of the Darkest Timeline?", msg.Text)
	require.Equal(t, "Abed Nadir", msg.User)
	require.Equal(t, "dreamatorium", msg.Source)
	require.Equal(t, "study-group-7", msg.SessionID)
}

func TestPostMessages_EmptyText(t *testing.T) {
	hub := NewHub()
	srv := newTestServer(hub, "study-group-7")
	defer srv.Close()

	payload := `{"text":"","user":"Jeff Winger"}`
	resp, err := http.Post(srv.URL+"/messages", "application/json", strings.NewReader(payload))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestPostMessages_InvalidJSON(t *testing.T) {
	hub := NewHub()
	srv := newTestServer(hub, "study-group-7")
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/messages", "application/json", strings.NewReader("{not json"))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestPostMessages_Defaults(t *testing.T) {
	hub := NewHub()
	srv := newTestServer(hub, "study-group-7")
	defer srv.Close()

	payload := `{"text":"I am the Truest Repairman"}`
	resp, err := http.Post(srv.URL+"/messages", "application/json", strings.NewReader(payload))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	msg := hub.PullMessage()
	require.Equal(t, "anonymous", msg.User)
	require.Equal(t, "api", msg.Source)
}

func TestSSE_EventDelivery(t *testing.T) {
	hub := NewHub()
	srv := newTestServer(hub, "paintball-101")
	defer srv.Close()

	// Start SSE connection
	resp, err := http.Get(srv.URL + "/events")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	// Publish an event after a short delay to let the SSE handler subscribe.
	go func() {
		time.Sleep(50 * time.Millisecond)
		hub.PublishEvent(types.OutboundEvent{
			ID:        "evt-paintball",
			SessionID: "paintball-101",
			Type:      "text",
			Content:   "Welcome to the thunderdome.",
			Timestamp: time.Now().UnixMilli(),
		})
	}()

	// Read the SSE event from the response body.
	scanner := bufio.NewScanner(resp.Body)
	var dataLine string
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for SSE event")
		default:
		}

		if !scanner.Scan() {
			t.Fatal("SSE stream closed before receiving event")
		}
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			dataLine = strings.TrimPrefix(line, "data: ")
			break
		}
	}

	var event types.OutboundEvent
	err = json.Unmarshal([]byte(dataLine), &event)
	require.NoError(t, err)
	require.Equal(t, "evt-paintball", event.ID)
	require.Equal(t, "paintball-101", event.SessionID)
	require.Equal(t, "text", event.Type)
	require.Equal(t, "Welcome to the thunderdome.", event.Content)
}
