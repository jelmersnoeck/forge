package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/engine"
	"github.com/jelmersnoeck/forge/internal/tracker"
	"github.com/jelmersnoeck/forge/pkg/config"
)

// mockServerAgent implements agent.Agent for server tests.
type mockServerAgent struct{}

func (m *mockServerAgent) Run(_ context.Context, _ agent.Request) (*agent.Response, error) {
	return &agent.Response{Output: "mock output"}, nil
}

func newTestEngine(t *testing.T) *engine.Engine {
	t.Helper()
	eng, err := engine.New(&engine.EngineConfig{
		MaxIterations: 3,
	}, map[string]agent.Agent{"mock": &mockServerAgent{}}, map[string]tracker.Tracker{}, nil)
	if err != nil {
		t.Fatalf("creating test engine: %v", err)
	}
	return eng
}

func newFullTestServer() *Server {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.ServerConfig{
		Port:           8080,
		AllowedOrigins: []string{"https://example.com"},
		Webhooks: config.WebhookConfig{
			GitHub: &config.GitHubWebhookConfig{
				Secret: "test-secret",
			},
		},
	}
	eng, _ := engine.New(&engine.EngineConfig{
		MaxIterations: 3,
	}, map[string]agent.Agent{"mock": &mockServerAgent{}}, map[string]tracker.Tracker{}, nil)

	return New(eng, cfg, logger)
}

func TestRoutes_Healthz(t *testing.T) {
	s := newFullTestServer()
	handler := s.routes()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got %q", resp["status"])
	}
}

func TestRoutes_CORS_AllowedOrigin(t *testing.T) {
	s := newFullTestServer()
	handler := s.routes()

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/build", nil)
	req.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "https://example.com" {
		t.Errorf("expected CORS Allow-Origin to be https://example.com, got %q", w.Header().Get("Access-Control-Allow-Origin"))
	}
	if w.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("expected CORS Allow-Methods header")
	}
}

func TestRoutes_CORS_DisallowedOrigin(t *testing.T) {
	s := newFullTestServer()
	handler := s.routes()

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/build", nil)
	req.Header.Set("Origin", "https://evil.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("expected no CORS Allow-Origin header for disallowed origin, got %q", w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestRoutes_CORS_NoOriginHeader(t *testing.T) {
	s := newFullTestServer()
	handler := s.routes()

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/build", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("expected no CORS Allow-Origin header when no Origin is sent, got %q", w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestRoutes_CORS_EmptyConfig(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.ServerConfig{Port: 8080}
	eng, _ := engine.New(&engine.EngineConfig{MaxIterations: 3}, map[string]agent.Agent{}, map[string]tracker.Tracker{}, nil)
	s := New(eng, cfg, logger)
	handler := s.routes()

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/build", nil)
	req.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("expected no CORS Allow-Origin when no origins configured, got %q", w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestRoutes_RequestID(t *testing.T) {
	s := newFullTestServer()
	handler := s.routes()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("X-Request-ID") == "" {
		t.Error("expected X-Request-ID header")
	}
}

func TestRoutes_RequestID_PassThrough(t *testing.T) {
	s := newFullTestServer()
	handler := s.routes()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-ID", "custom-id-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("X-Request-ID") != "custom-id-123" {
		t.Errorf("expected X-Request-ID to be passed through, got %q", w.Header().Get("X-Request-ID"))
	}
}

func TestRoutes_PanicRecovery(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /panic", func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	handler := panicRecovery(logger)(mux)

	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}

	// Ensure no stack trace leaks in the body.
	body := w.Body.String()
	if len(body) > 200 {
		t.Errorf("response body too long, may contain stack trace: %s", body[:200])
	}
}

func TestNew(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg := &config.ServerConfig{Port: 9090}
	eng := newTestEngine(t)

	s := New(eng, cfg, logger)
	if s == nil {
		t.Fatal("expected non-nil server")
	}
	if s.engine != eng {
		t.Error("engine not set")
	}
	if s.config != cfg {
		t.Error("config not set")
	}
	if s.jobs == nil {
		t.Error("job queue not initialized")
	}
	if s.broker == nil {
		t.Error("SSE broker not initialized")
	}
}
