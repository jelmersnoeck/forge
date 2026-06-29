package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log"
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
	mux.HandleFunc("POST /review", handleReview(hub, sessionID))
	mux.HandleFunc("POST /interrupt", handleInterrupt(hub))
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
	msg, _ := hub.PullMessage(context.Background())
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

	msg, _ := hub.PullMessage(context.Background())
	require.Equal(t, "anonymous", msg.User)
	require.Equal(t, "api", msg.Source)
}

func TestSSE_EventDelivery(t *testing.T) {
	hub := NewHub()

	// Signal when the SSE handler subscribes to the hub.
	subscribed := make(chan struct{}, 1)
	hub.OnSubscribe(func() { subscribed <- struct{}{} })

	srv := newTestServer(hub, "paintball-101")
	defer srv.Close()

	// Start SSE connection
	resp, err := http.Get(srv.URL + "/events")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	// Wait for handler to subscribe before publishing.
	<-subscribed

	hub.PublishEvent(types.OutboundEvent{
		ID:        "evt-paintball",
		SessionID: "paintball-101",
		Type:      "text",
		Content:   "Welcome to the thunderdome.",
		Timestamp: time.Now().UnixMilli(),
	})

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

func TestPostReview_Endpoint(t *testing.T) {
	tests := map[string]struct {
		body string
		want string
	}{
		"explicit base":  {body: `{"base":"main"}`, want: "main"},
		"empty body":     {body: ``, want: ""},
		"malformed JSON": {body: `{oops`, want: ""},
		"feature base":   {body: `{"base":"jelmer/greendale"}`, want: "jelmer/greendale"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			hub := NewHub()
			srv := newTestServer(hub, "review-101")
			defer srv.Close()

			resp, err := http.Post(srv.URL+"/review", "application/json", strings.NewReader(tc.body))
			r.NoError(err)
			defer func() { _ = resp.Body.Close() }()

			r.Equal(http.StatusAccepted, resp.StatusCode)

			var out map[string]string
			r.NoError(json.NewDecoder(resp.Body).Decode(&out))
			r.Equal("review_started", out["status"])

			select {
			case got := <-hub.ReviewChannel():
				r.Equal(tc.want, got)
			case <-time.After(time.Second):
				r.Fail("review channel did not receive value")
			}
		})
	}
}

// TestPostReview_MalformedBody_LogsWarning verifies that a malformed (non-empty)
// POST /review body is surfaced via a warning log for operational visibility,
// while still returning 202 and auto-detecting the base. An empty body (the
// common case) must NOT log, since it is expected.
func TestPostReview_MalformedBody_LogsWarning(t *testing.T) {
	tests := map[string]struct {
		body        string
		wantLogged  bool
		wantContent string
	}{
		"malformed body logs": {body: `{oops`, wantLogged: true, wantContent: "malformed POST /review body"},
		"empty body silent":   {body: ``, wantLogged: false},
		"valid body silent":   {body: `{"base":"greendale"}`, wantLogged: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			var logBuf bytes.Buffer
			origOut := log.Writer()
			origFlags := log.Flags()
			log.SetOutput(&logBuf)
			log.SetFlags(0)
			defer func() {
				log.SetOutput(origOut)
				log.SetFlags(origFlags)
			}()

			hub := NewHub()
			srv := newTestServer(hub, "review-malformed-101")
			defer srv.Close()

			resp, err := http.Post(srv.URL+"/review", "application/json", strings.NewReader(tc.body))
			r.NoError(err)
			defer func() { _ = resp.Body.Close() }()
			r.Equal(http.StatusAccepted, resp.StatusCode)

			// Drain the review trigger so the handler's TriggerReview does not leak.
			select {
			case <-hub.ReviewChannel():
			case <-time.After(time.Second):
				r.Fail("review channel did not receive value")
			}

			logged := logBuf.String()
			if tc.wantLogged {
				r.Contains(logged, tc.wantContent)
				r.Contains(logged, "review-malformed-101")
			} else {
				r.Empty(logged, "expected no log output for non-malformed body")
			}
		})
	}
}

func TestPostInterrupt_Endpoint(t *testing.T) {
	r := require.New(t)
	hub := NewHub()
	srv := newTestServer(hub, "interrupt-101")
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/interrupt", "application/json", strings.NewReader(""))
	r.NoError(err)
	defer func() { _ = resp.Body.Close() }()

	r.Equal(http.StatusAccepted, resp.StatusCode)

	var out map[string]string
	r.NoError(json.NewDecoder(resp.Body).Decode(&out))
	r.Equal("interrupted", out["status"])

	select {
	case <-hub.InterruptChannel():
		// good — interrupt delivered
	case <-time.After(time.Second):
		r.Fail("interrupt channel did not receive signal")
	}
}

func TestSetModel_Endpoint(t *testing.T) {
	r := require.New(t)
	hub := NewHub()
	worker := &Worker{}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /model", handleSetModel(worker, hub))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Subscribe to SSE events to verify model event is emitted
	events, unsub := hub.Subscribe()
	defer unsub()

	payload := `{"model":"sonnet"}`
	resp, err := http.Post(srv.URL+"/model", "application/json", strings.NewReader(payload))
	r.NoError(err)
	defer func() { _ = resp.Body.Close() }()

	r.Equal(http.StatusOK, resp.StatusCode)

	var body map[string]string
	err = json.NewDecoder(resp.Body).Decode(&body)
	r.NoError(err)
	r.Equal("sonnet", body["model"])
	r.Equal("sonnet", worker.ModelOverride())

	// Verify model event was emitted
	select {
	case evt := <-events:
		r.Equal("model", evt.Type)
		r.Equal("sonnet", evt.Content)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for model event")
	}
}

func TestSetModel_EmptyModel(t *testing.T) {
	r := require.New(t)
	hub := NewHub()
	worker := &Worker{}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /model", handleSetModel(worker, hub))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/model", "application/json", strings.NewReader(`{"model":""}`))
	r.NoError(err)
	defer func() { _ = resp.Body.Close() }()

	r.Equal(http.StatusBadRequest, resp.StatusCode)
}

func TestSetModel_InvalidJSON(t *testing.T) {
	r := require.New(t)
	hub := NewHub()
	worker := &Worker{}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /model", handleSetModel(worker, hub))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/model", "application/json", strings.NewReader("{oops"))
	r.NoError(err)
	defer func() { _ = resp.Body.Close() }()

	r.Equal(http.StatusBadRequest, resp.StatusCode)
}

func TestListModels_Endpoint(t *testing.T) {
	r := require.New(t)
	worker := &Worker{}
	worker.providers = map[string]types.LLMProvider{}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /models", handleListModels(worker))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/models")
	r.NoError(err)
	defer func() { _ = resp.Body.Close() }()

	r.Equal(http.StatusOK, resp.StatusCode)
	r.Equal("application/json", resp.Header.Get("Content-Type"))

	var result []types.ProviderModels
	err = json.NewDecoder(resp.Body).Decode(&result)
	r.NoError(err)
	// Empty providers → empty list
	r.Empty(result)
}
