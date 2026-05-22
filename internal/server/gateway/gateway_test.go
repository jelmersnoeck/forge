package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jelmersnoeck/forge/internal/server/backend"
	"github.com/jelmersnoeck/forge/internal/server/bus"
	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

// fakeBackend implements backend.Backend for testing without tmux.
type fakeBackend struct {
	mu     sync.Mutex
	agents map[string]string // sessionID -> address
	err    error             // returned by EnsureAgent if set
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{agents: make(map[string]string)}
}

func (f *fakeBackend) EnsureAgent(_ context.Context, sessionID string, _ backend.AgentOptions) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	if addr, ok := f.agents[sessionID]; ok {
		return addr, nil
	}
	return "", fmt.Errorf("no agent configured for session %s", sessionID)
}

func (f *fakeBackend) StopAgent(_ context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.agents, sessionID)
	return nil
}

func (f *fakeBackend) AgentAddress(sessionID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.agents[sessionID]
}

func (f *fakeBackend) Close() error { return nil }

func (f *fakeBackend) setAgent(sessionID, addr string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agents[sessionID] = addr
}

// newTestMux creates a ServeMux wired to gateway handlers with the given config.
func newTestMux(cfg Config) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sessions", handleCreateSession)
	mux.HandleFunc("GET /sessions/{sessionId}", handleGetSession)
	mux.HandleFunc("POST /sessions/{sessionId}/messages", handleSendMessage(cfg))
	mux.HandleFunc("POST /sessions/{sessionId}/review", handleReview(cfg))
	mux.HandleFunc("POST /sessions/{sessionId}/interrupt", handleInterrupt(cfg))
	mux.HandleFunc("GET /sessions/{sessionId}/events", handleEvents)
	return mux
}

func TestHandleCreateSession(t *testing.T) {
	tests := map[string]struct {
		body       string
		wantStatus int
	}{
		"with cwd and metadata": {
			body:       `{"cwd":"/tmp/greendale","metadata":{"course":"spanish 101"}}`,
			wantStatus: http.StatusCreated,
		},
		"empty body": {
			body:       ``,
			wantStatus: http.StatusCreated,
		},
		"no metadata": {
			body:       `{"cwd":"/tmp/greendale"}`,
			wantStatus: http.StatusCreated,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			be := newFakeBackend()
			cfg := Config{WorkspaceDir: "/tmp/workspace", Backend: be}
			mux := newTestMux(cfg)

			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/sessions", strings.NewReader(tc.body))
			mux.ServeHTTP(w, req)

			r.Equal(tc.wantStatus, w.Code)

			var resp map[string]any
			r.NoError(json.NewDecoder(w.Body).Decode(&resp))
			r.NotEmpty(resp["sessionId"])

			// Session should be stored in bus
			sid := resp["sessionId"].(string)
			meta := bus.GetSession(sid)
			r.NotNil(meta)
			r.Equal(sid, meta.SessionID)
			r.Greater(meta.CreatedAt, int64(0))
		})
	}
}

func TestHandleGetSession(t *testing.T) {
	tests := map[string]struct {
		setup      func() string // returns sessionID
		wantStatus int
	}{
		"existing session": {
			setup: func() string {
				sid := "troy-barnes-101"
				bus.SetSession(&types.SessionMeta{
					SessionID:    sid,
					CWD:          "/tmp/greendale",
					Metadata:     map[string]any{"dean": "pelton"},
					CreatedAt:    time.Now().UnixMilli(),
					LastActiveAt: time.Now().UnixMilli(),
				})
				return sid
			},
			wantStatus: http.StatusOK,
		},
		"unknown session": {
			setup:      func() string { return "nonexistent-session-id" },
			wantStatus: http.StatusNotFound,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			be := newFakeBackend()
			cfg := Config{WorkspaceDir: "/tmp/workspace", Backend: be}
			mux := newTestMux(cfg)

			sid := tc.setup()
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/sessions/"+sid, nil)
			mux.ServeHTTP(w, req)

			r.Equal(tc.wantStatus, w.Code)

			if tc.wantStatus == http.StatusOK {
				var meta types.SessionMeta
				r.NoError(json.NewDecoder(w.Body).Decode(&meta))
				r.Equal(sid, meta.SessionID)
			}
		})
	}
}

func TestHandleSendMessage_EmptyText(t *testing.T) {
	r := require.New(t)
	be := newFakeBackend()
	cfg := Config{WorkspaceDir: "/tmp/workspace", Backend: be}
	mux := newTestMux(cfg)

	w := httptest.NewRecorder()
	body := `{"text":"","user":"troy"}`
	req := httptest.NewRequest("POST", "/sessions/abed-nadir/messages", strings.NewReader(body))
	mux.ServeHTTP(w, req)

	r.Equal(http.StatusBadRequest, w.Code)
	r.Contains(w.Body.String(), "text is required")
}

func TestHandleSendMessage_InvalidJSON(t *testing.T) {
	r := require.New(t)
	be := newFakeBackend()
	cfg := Config{WorkspaceDir: "/tmp/workspace", Backend: be}
	mux := newTestMux(cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/sessions/abed-nadir/messages", strings.NewReader("not json"))
	mux.ServeHTTP(w, req)

	r.Equal(http.StatusBadRequest, w.Code)
	r.Contains(w.Body.String(), "invalid JSON")
}

func TestHandleSendMessage_AgentStartFailure(t *testing.T) {
	r := require.New(t)
	be := newFakeBackend()
	be.err = fmt.Errorf("tmux not found")
	cfg := Config{WorkspaceDir: "/tmp/workspace", Backend: be}
	mux := newTestMux(cfg)

	w := httptest.NewRecorder()
	body := `{"text":"hello"}`
	req := httptest.NewRequest("POST", "/sessions/abed-nadir/messages", strings.NewReader(body))
	mux.ServeHTTP(w, req)

	r.Equal(http.StatusInternalServerError, w.Code)
	r.Contains(w.Body.String(), "failed to start agent")
}

func TestHandleSendMessage_ForwardsToAgent(t *testing.T) {
	r := require.New(t)

	// Fake agent HTTP server
	agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/messages":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
		case "/events":
			// SSE endpoint — just close immediately
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer agentServer.Close()

	// Strip "http://" to get "host:port"
	agentAddr := strings.TrimPrefix(agentServer.URL, "http://")

	be := newFakeBackend()
	be.setAgent("study-group", agentAddr)
	cfg := Config{WorkspaceDir: "/tmp/workspace", SessionsDir: "/tmp/sessions", Backend: be}
	mux := newTestMux(cfg)

	// Store session so CWD is resolved
	bus.SetSession(&types.SessionMeta{
		SessionID: "study-group",
		CWD:       "/tmp/greendale",
		Metadata:  map[string]any{},
		CreatedAt: time.Now().UnixMilli(),
	})

	w := httptest.NewRecorder()
	body := `{"text":"cool cool cool"}`
	req := httptest.NewRequest("POST", "/sessions/study-group/messages", strings.NewReader(body))
	mux.ServeHTTP(w, req)

	r.Equal(http.StatusAccepted, w.Code)

	var resp map[string]string
	r.NoError(json.NewDecoder(w.Body).Decode(&resp))
	r.Equal("queued", resp["status"])
}

func TestHandleSendMessage_AgentForwardFailure(t *testing.T) {
	r := require.New(t)

	// Use an address that will refuse connections
	be := newFakeBackend()
	be.setAgent("study-group", "127.0.0.1:1") // port 1 — should refuse
	cfg := Config{WorkspaceDir: "/tmp/workspace", SessionsDir: "/tmp/sessions", Backend: be}
	mux := newTestMux(cfg)

	w := httptest.NewRecorder()
	body := `{"text":"hello"}`
	req := httptest.NewRequest("POST", "/sessions/study-group/messages", strings.NewReader(body))
	mux.ServeHTTP(w, req)

	r.Equal(http.StatusBadGateway, w.Code)
	r.Contains(w.Body.String(), "failed to forward message")
}

func TestHandleReview_ForwardsToAgent(t *testing.T) {
	r := require.New(t)

	agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/review":
			body, _ := io.ReadAll(req.Body)
			var payload map[string]string
			_ = json.Unmarshal(body, &payload)
			r.Equal("main", payload["base"])

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "review_started"})
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer agentServer.Close()

	agentAddr := strings.TrimPrefix(agentServer.URL, "http://")
	be := newFakeBackend()
	be.setAgent("review-session", agentAddr)
	cfg := Config{WorkspaceDir: "/tmp/workspace", SessionsDir: "/tmp/sessions", Backend: be}
	mux := newTestMux(cfg)

	w := httptest.NewRecorder()
	body := `{"base":"main"}`
	req := httptest.NewRequest("POST", "/sessions/review-session/review", strings.NewReader(body))
	mux.ServeHTTP(w, req)

	r.Equal(http.StatusAccepted, w.Code)

	var resp map[string]string
	r.NoError(json.NewDecoder(w.Body).Decode(&resp))
	r.Equal("review_started", resp["status"])
}

func TestHandleInterrupt_NoAgent(t *testing.T) {
	r := require.New(t)
	be := newFakeBackend()
	cfg := Config{WorkspaceDir: "/tmp/workspace", Backend: be}
	mux := newTestMux(cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/sessions/no-agent-here/interrupt", nil)
	mux.ServeHTTP(w, req)

	r.Equal(http.StatusOK, w.Code)
	var resp map[string]string
	r.NoError(json.NewDecoder(w.Body).Decode(&resp))
	r.Equal("no active agent", resp["status"])
}

func TestHandleInterrupt_ForwardsToAgent(t *testing.T) {
	r := require.New(t)

	agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer agentServer.Close()

	agentAddr := strings.TrimPrefix(agentServer.URL, "http://")
	be := newFakeBackend()
	be.setAgent("active-session", agentAddr)
	cfg := Config{WorkspaceDir: "/tmp/workspace", Backend: be}
	mux := newTestMux(cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/sessions/active-session/interrupt", nil)
	mux.ServeHTTP(w, req)

	r.Equal(http.StatusAccepted, w.Code)
	var resp map[string]string
	r.NoError(json.NewDecoder(w.Body).Decode(&resp))
	r.Equal("interrupted", resp["status"])
}

func TestHandleEvents_SSE(t *testing.T) {
	r := require.New(t)
	be := newFakeBackend()
	cfg := Config{WorkspaceDir: "/tmp/workspace", Backend: be}
	mux := newTestMux(cfg)

	server := httptest.NewServer(mux)
	defer server.Close()

	// Publish an event in the background after a brief delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		bus.PublishEvent("sse-session", types.OutboundEvent{
			ID:        "evt-1",
			SessionID: "sse-session",
			Type:      "text",
			Content:   "six seasons and a movie",
			Timestamp: time.Now().UnixMilli(),
		})
	}()

	// Connect SSE
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/sessions/sse-session/events", nil)
	resp, err := http.DefaultClient.Do(req)
	r.NoError(err)
	defer func() { _ = resp.Body.Close() }()

	r.Equal(http.StatusOK, resp.StatusCode)
	r.Equal("text/event-stream", resp.Header.Get("Content-Type"))

	// Read one event
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	output := string(buf[:n])
	r.Contains(output, "id: evt-1")
	r.Contains(output, "six seasons and a movie")
}

func TestStartRelay_Idempotent(t *testing.T) {
	r := require.New(t)

	// Fake agent SSE endpoint
	agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", 500)
			return
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n",
			`{"id":"r-1","sessionId":"relay-test","type":"text","content":"streets ahead","timestamp":1234}`)
		flusher.Flush()
	}))
	defer agentServer.Close()

	agentAddr := strings.TrimPrefix(agentServer.URL, "http://")

	// Subscribe before starting relay
	events, unsub := bus.Subscribe("relay-test")
	defer unsub()

	// Call startRelay twice — second call should be no-op
	startRelay("relay-test", agentAddr)
	startRelay("relay-test", agentAddr)

	// Read the relayed event
	select {
	case evt := <-events:
		r.Equal("r-1", evt.ID)
		r.Equal("streets ahead", evt.Content)
	case <-time.After(2 * time.Second):
		r.Fail("timed out waiting for relayed event")
	}

	// Clean up relay entry (it auto-cleans when SSE closes)
	time.Sleep(200 * time.Millisecond)
}

func TestIsDataLine(t *testing.T) {
	tests := map[string]struct {
		input string
		want  bool
	}{
		"valid data line":    {input: `data: {"type":"text"}`, want: true},
		"comment":            {input: ": keep-alive", want: false},
		"id line":            {input: "id: 123", want: false},
		"empty":              {input: "", want: false},
		"short":              {input: "data:", want: false},
		"data prefix no val": {input: "data: ", want: false},
		"data with content":  {input: "data: x", want: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, isDataLine(tc.input))
		})
	}
}

func TestHandleSendMessage_DefaultsUserAndSource(t *testing.T) {
	r := require.New(t)

	var receivedBody map[string]string
	agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/messages":
			body, _ := io.ReadAll(req.Body)
			_ = json.Unmarshal(body, &receivedBody)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer agentServer.Close()

	agentAddr := strings.TrimPrefix(agentServer.URL, "http://")
	be := newFakeBackend()
	be.setAgent("defaults-test", agentAddr)
	cfg := Config{WorkspaceDir: "/tmp/workspace", SessionsDir: "/tmp/sessions", Backend: be}
	mux := newTestMux(cfg)

	w := httptest.NewRecorder()
	// Only provide text — user and source should default
	body := `{"text":"pop pop"}`
	req := httptest.NewRequest("POST", "/sessions/defaults-test/messages", strings.NewReader(body))
	mux.ServeHTTP(w, req)

	r.Equal(http.StatusAccepted, w.Code)
	r.Equal("anonymous", receivedBody["user"])
	r.Equal("api", receivedBody["source"])
}

func TestHandleSendMessage_CWDFallsBackToWorkspace(t *testing.T) {
	r := require.New(t)

	var receivedMessage bool
	agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/messages":
			receivedMessage = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer agentServer.Close()

	agentAddr := strings.TrimPrefix(agentServer.URL, "http://")
	be := newFakeBackend()
	be.setAgent("unknown-session", agentAddr)
	cfg := Config{WorkspaceDir: "/tmp/workspace", SessionsDir: "/tmp/sessions", Backend: be}
	mux := newTestMux(cfg)

	// Don't create a session in bus — message for unknown session ID
	w := httptest.NewRecorder()
	body := `{"text":"hello"}`
	req := httptest.NewRequest("POST", "/sessions/unknown-session/messages", strings.NewReader(body))
	mux.ServeHTTP(w, req)

	r.Equal(http.StatusAccepted, w.Code)
	r.True(receivedMessage)
}

func TestHandleSendMessage_UpdatesLastActiveAt(t *testing.T) {
	r := require.New(t)

	agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/messages":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer agentServer.Close()

	agentAddr := strings.TrimPrefix(agentServer.URL, "http://")
	be := newFakeBackend()
	be.setAgent("active-test", agentAddr)
	cfg := Config{WorkspaceDir: "/tmp/workspace", SessionsDir: "/tmp/sessions", Backend: be}
	mux := newTestMux(cfg)

	origTime := time.Now().Add(-1 * time.Hour).UnixMilli()
	bus.SetSession(&types.SessionMeta{
		SessionID:    "active-test",
		CWD:          "/tmp/greendale",
		Metadata:     map[string]any{},
		CreatedAt:    origTime,
		LastActiveAt: origTime,
	})

	w := httptest.NewRecorder()
	body := `{"text":"hello"}`
	req := httptest.NewRequest("POST", "/sessions/active-test/messages", strings.NewReader(body))
	mux.ServeHTTP(w, req)

	r.Equal(http.StatusAccepted, w.Code)

	meta := bus.GetSession("active-test")
	r.NotNil(meta)
	r.Greater(meta.LastActiveAt, origTime, "LastActiveAt should be updated")
}

func TestHandleCreateSession_ResponseFormat(t *testing.T) {
	r := require.New(t)
	be := newFakeBackend()
	cfg := Config{WorkspaceDir: "/tmp/workspace", Backend: be}
	mux := newTestMux(cfg)

	w := httptest.NewRecorder()
	body := `{"cwd":"/tmp/greendale","metadata":{"dean":"pelton"}}`
	req := httptest.NewRequest("POST", "/sessions", strings.NewReader(body))
	mux.ServeHTTP(w, req)

	r.Equal(http.StatusCreated, w.Code)
	r.Equal("application/json", w.Header().Get("Content-Type"))

	var resp map[string]any
	r.NoError(json.NewDecoder(w.Body).Decode(&resp))

	// Must have sessionId and metadata
	r.Contains(resp, "sessionId")
	r.Contains(resp, "metadata")

	// sessionId should be a valid UUID (36 chars with dashes)
	sid := resp["sessionId"].(string)
	r.Len(sid, 36)

	meta := resp["metadata"].(map[string]any)
	r.Equal("pelton", meta["dean"])
}

func TestHandleGetSession_404Format(t *testing.T) {
	r := require.New(t)
	be := newFakeBackend()
	cfg := Config{WorkspaceDir: "/tmp/workspace", Backend: be}
	mux := newTestMux(cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/sessions/nonexistent", nil)
	mux.ServeHTTP(w, req)

	r.Equal(http.StatusNotFound, w.Code)
	r.Contains(w.Body.String(), "session not found")
}

func TestHandleEvents_Headers(t *testing.T) {
	r := require.New(t)
	be := newFakeBackend()
	cfg := Config{WorkspaceDir: "/tmp/workspace", Backend: be}
	mux := newTestMux(cfg)

	server := httptest.NewServer(mux)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/sessions/header-test/events", nil)
	resp, err := http.DefaultClient.Do(req)
	r.NoError(err)
	defer func() { _ = resp.Body.Close() }()

	r.Equal("text/event-stream", resp.Header.Get("Content-Type"))
	r.Equal("no-cache", resp.Header.Get("Cache-Control"))
	r.Equal("keep-alive", resp.Header.Get("Connection"))
}

func TestHandleReview_EmptyBody(t *testing.T) {
	r := require.New(t)

	agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/review":
			// Should receive {} when body is empty
			body, _ := io.ReadAll(req.Body)
			r.Equal("{}", string(body))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer agentServer.Close()

	agentAddr := strings.TrimPrefix(agentServer.URL, "http://")
	be := newFakeBackend()
	be.setAgent("review-empty", agentAddr)
	cfg := Config{WorkspaceDir: "/tmp/workspace", SessionsDir: "/tmp/sessions", Backend: be}
	mux := newTestMux(cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/sessions/review-empty/review", bytes.NewReader(nil))
	mux.ServeHTTP(w, req)

	r.Equal(http.StatusAccepted, w.Code)
}
