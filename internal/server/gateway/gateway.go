// Package gateway provides the HTTP API for the forge gateway.
//
//	POST   /sessions                     create session
//	GET    /sessions/:sessionId          get session info
//	POST   /sessions/:sessionId/messages send message
//	GET    /sessions/:sessionId/events   SSE stream
package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jelmersnoeck/forge/internal/server/backend"
	"github.com/jelmersnoeck/forge/internal/server/bus"
	"github.com/jelmersnoeck/forge/internal/types"
)

// Config holds gateway settings.
type Config struct {
	Port         int
	Host         string
	WorkspaceDir string
	SessionsDir  string
	Backend      backend.Backend
}

// Start launches the HTTP server.
func Start(cfg Config) error {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /sessions", handleCreateSession)
	mux.HandleFunc("GET /sessions/{sessionId}", handleGetSession)
	mux.HandleFunc("POST /sessions/{sessionId}/messages", handleSendMessage(cfg))
	mux.HandleFunc("POST /sessions/{sessionId}/review", handleReview(cfg))
	mux.HandleFunc("POST /sessions/{sessionId}/interrupt", handleInterrupt(cfg))
	mux.HandleFunc("GET /sessions/{sessionId}/events", handleEvents)

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	log.Printf("[gateway] listening on %s", addr)
	return http.ListenAndServe(addr, mux)
}

func handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CWD      string         `json:"cwd"`
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		body.Metadata = make(map[string]any)
	}

	sessionID := uuid.New().String()
	now := time.Now().UnixMilli()
	meta := &types.SessionMeta{
		SessionID:    sessionID,
		CWD:          body.CWD,
		Metadata:     body.Metadata,
		CreatedAt:    now,
		LastActiveAt: now,
	}
	bus.SetSession(meta)

	log.Printf("[gateway] session created id=%s cwd=%s", sessionID, body.CWD)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"sessionId": sessionID,
		"metadata":  body.Metadata,
	})
}

func handleGetSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionId")
	meta := bus.GetSession(sessionID)
	if meta == nil {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(meta)
}

// relayEntry tracks an active SSE relay including its cancel function.
type relayEntry struct {
	cancel context.CancelFunc
}

// relays tracks active SSE relay goroutines per session so we don't
// start duplicates.
var (
	relays   = make(map[string]*relayEntry)
	relaysMu sync.Mutex
)

// sseClient is shared across relays with timeouts that prevent hanging
// on dead connections. The ResponseHeaderTimeout catches the initial
// connect; the transport's DialContext timeout covers DNS/TCP.
var sseClient = &http.Client{
	Timeout: 0, // no overall timeout — SSE streams are long-lived
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
		}).DialContext,
		ResponseHeaderTimeout: 10 * time.Second,
		IdleConnTimeout:       0, // keep SSE connection open
	},
}

const (
	relayReconnectBase = 1 * time.Second
	relayReconnectMax  = 30 * time.Second
	relayMaxRetries    = 10
)

// startRelay connects to an agent's /events SSE endpoint and republishes
// events via the server bus so CLI clients see them. Automatically
// reconnects on transient failures with exponential backoff.
func startRelay(sessionID, agentAddr string) {
	relaysMu.Lock()
	if _, ok := relays[sessionID]; ok {
		relaysMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	relays[sessionID] = &relayEntry{cancel: cancel}
	relaysMu.Unlock()

	go func() {
		defer func() {
			relaysMu.Lock()
			delete(relays, sessionID)
			relaysMu.Unlock()
		}()

		retries := 0
		for {
			err := relayOnce(ctx, sessionID, agentAddr)
			if ctx.Err() != nil {
				log.Printf("[relay:%s] stopped", sessionID)
				return
			}
			retries++
			if retries > relayMaxRetries {
				log.Printf("[relay:%s] giving up after %d retries: %v", sessionID, relayMaxRetries, err)
				return
			}
			delay := relayBackoff(retries)
			log.Printf("[relay:%s] connection lost (%v), reconnecting in %s (attempt %d/%d)",
				sessionID, err, delay, retries, relayMaxRetries)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
		}
	}()
}

// relayOnce runs a single SSE connection. Returns when the connection
// drops or the context is cancelled.
func relayOnce(ctx context.Context, sessionID, agentAddr string) error {
	url := fmt.Sprintf("http://%s/events", agentAddr)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := sseClient.Do(req)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !isDataLine(line) {
			continue
		}
		jsonData := line[6:] // strip "data: " prefix

		var event types.OutboundEvent
		if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
			continue
		}
		bus.PublishEvent(sessionID, event)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	return fmt.Errorf("stream closed by server")
}

// relayBackoff returns the delay before the n-th reconnection attempt.
func relayBackoff(attempt int) time.Duration {
	d := relayReconnectBase
	for i := 1; i < attempt; i++ {
		d *= 2
		if d > relayReconnectMax {
			return relayReconnectMax
		}
	}
	return d
}

func isDataLine(line string) bool {
	return len(line) > 6 && line[:6] == "data: "
}

func handleSendMessage(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.PathValue("sessionId")

		var body struct {
			Text     string         `json:"text"`
			User     string         `json:"user"`
			Source   string         `json:"source"`
			Metadata map[string]any `json:"metadata"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
		if body.Text == "" {
			http.Error(w, `{"error":"text is required"}`, http.StatusBadRequest)
			return
		}
		if body.User == "" {
			body.User = "anonymous"
		}
		if body.Source == "" {
			body.Source = "api"
		}

		// Ensure the agent is running via the backend.
		cwd := cfg.WorkspaceDir
		if meta := bus.GetSession(sessionID); meta != nil && meta.CWD != "" {
			cwd = meta.CWD
		}

		alreadyRunning := cfg.Backend.AgentAddress(sessionID) != ""
		agentAddr, err := cfg.Backend.EnsureAgent(r.Context(), sessionID, backend.AgentOptions{
			CWD:         cwd,
			SessionsDir: cfg.SessionsDir,
		})
		if err != nil {
			log.Printf("[gateway] backend.EnsureAgent error: %v", err)
			http.Error(w, fmt.Sprintf(`{"error":"failed to start agent: %v"}`, err), http.StatusInternalServerError)
			return
		}

		if alreadyRunning {
			log.Printf("[gateway] session resumed id=%s agent=%s", sessionID, agentAddr)
		} else {
			log.Printf("[gateway] agent started id=%s agent=%s cwd=%s", sessionID, agentAddr, cwd)
		}

		// Start SSE relay from agent → bus (idempotent).
		startRelay(sessionID, agentAddr)

		// Forward the message to the agent.
		msgBody, _ := json.Marshal(map[string]string{
			"text":   body.Text,
			"user":   body.User,
			"source": body.Source,
		})
		agentURL := fmt.Sprintf("http://%s/messages", agentAddr)
		resp, err := http.Post(agentURL, "application/json", bytes.NewReader(msgBody))
		if err != nil {
			log.Printf("[gateway] forward to agent error: %v", err)
			http.Error(w, `{"error":"failed to forward message to agent"}`, http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()

		// Parse the agent's response to get the actual status
		var agentResp map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&agentResp); err != nil {
			log.Printf("[gateway] failed to decode agent response: %v", err)
			agentResp = map[string]string{"status": "queued"} // fallback
		}

		// Update session metadata.
		meta := bus.GetSession(sessionID)
		if meta != nil {
			meta.LastActiveAt = time.Now().UnixMilli()
			bus.SetSession(meta)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(agentResp)
	}
}

func handleReview(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.PathValue("sessionId")

		// Ensure agent is running.
		cwd := cfg.WorkspaceDir
		if meta := bus.GetSession(sessionID); meta != nil && meta.CWD != "" {
			cwd = meta.CWD
		}

		agentAddr, err := cfg.Backend.EnsureAgent(r.Context(), sessionID, backend.AgentOptions{
			CWD:         cwd,
			SessionsDir: cfg.SessionsDir,
		})
		if err != nil {
			log.Printf("[gateway] backend.EnsureAgent error: %v", err)
			http.Error(w, fmt.Sprintf(`{"error":"failed to start agent: %v"}`, err), http.StatusInternalServerError)
			return
		}

		// Start SSE relay from agent -> bus (idempotent).
		startRelay(sessionID, agentAddr)

		// Forward review request to agent.
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		if len(body) == 0 {
			body = []byte("{}")
		}

		agentURL := fmt.Sprintf("http://%s/review", agentAddr)
		resp, err := http.Post(agentURL, "application/json", bytes.NewReader(body))
		if err != nil {
			log.Printf("[gateway] forward review to agent error: %v", err)
			http.Error(w, `{"error":"failed to forward review to agent"}`, http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()

		log.Printf("[gateway] review started id=%s", sessionID)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "review_started"})
	}
}

func handleInterrupt(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.PathValue("sessionId")

		// Check if agent is running
		agentAddr := cfg.Backend.AgentAddress(sessionID)
		if agentAddr == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "no active agent"})
			return
		}

		// Forward interrupt to agent
		agentURL := fmt.Sprintf("http://%s/interrupt", agentAddr)
		resp, err := http.Post(agentURL, "application/json", bytes.NewReader([]byte("{}")))
		if err != nil {
			log.Printf("[gateway] forward interrupt to agent error: %v", err)
			http.Error(w, `{"error":"failed to forward interrupt to agent"}`, http.StatusBadGateway)
			return
		}
		_ = resp.Body.Close()

		log.Printf("[gateway] interrupt sent to agent id=%s", sessionID)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "interrupted"})
	}
}

func handleEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionId")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	events, unsub := bus.Subscribe(sessionID)
	defer unsub()

	for {
		select {
		case event, ok := <-events:
			if !ok {
				return
			}
			data, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "id: %s\ndata: %s\n\n", event.ID, data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
