package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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

// mockWebhookAgent implements agent.Agent for webhook tests.
type mockWebhookAgent struct{}

func (m *mockWebhookAgent) Run(_ context.Context, _ agent.Request) (*agent.Response, error) {
	return &agent.Response{Output: "mock"}, nil
}

func newTestServer(secret string) *Server {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.ServerConfig{
		Port: 8080,
		Webhooks: config.WebhookConfig{
			GitHub: &config.GitHubWebhookConfig{
				Secret: secret,
			},
		},
	}
	eng, _ := engine.New(&engine.EngineConfig{}, map[string]agent.Agent{"mock": &mockWebhookAgent{}}, map[string]tracker.Tracker{}, nil)

	broker := NewSSEBroker()
	queue := NewJobQueue(NewMemoryJobStore(), broker)

	return &Server{
		engine:  eng,
		config:  cfg,
		logger:  logger,
		jobs:    queue,
		broker:  broker,
		limiter: newRateLimiter(1000, 60_000_000_000), // high limit for tests
	}
}

func signPayload(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestWebhook_MissingSignature(t *testing.T) {
	s := newTestServer("test-secret")
	body := []byte(`{"action":"opened"}`)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issues")
	w := httptest.NewRecorder()

	s.handleGitHubWebhook(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}

	var resp APIError
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != "MISSING_SIGNATURE" {
		t.Errorf("expected code MISSING_SIGNATURE, got %q", resp.Code)
	}
}

func TestWebhook_InvalidSignature(t *testing.T) {
	s := newTestServer("test-secret")
	body := []byte(`{"action":"opened"}`)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issues")
	req.Header.Set("X-Hub-Signature-256", "sha256=0000000000000000000000000000000000000000000000000000000000000000")
	w := httptest.NewRecorder()

	s.handleGitHubWebhook(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}

	var resp APIError
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != "INVALID_SIGNATURE" {
		t.Errorf("expected code INVALID_SIGNATURE, got %q", resp.Code)
	}
}

func TestWebhook_ValidSignature_IssueOpened_WithForgeLabel(t *testing.T) {
	s := newTestServer("test-secret")

	payload := map[string]interface{}{
		"action": "opened",
		"issue": map[string]interface{}{
			"number": 42,
			"title":  "Test Issue",
			"labels": []interface{}{
				map[string]interface{}{"name": "forge"},
			},
		},
		"repository": map[string]interface{}{
			"full_name": "org/repo",
		},
	}
	body, _ := json.Marshal(payload)
	sig := signPayload(body, "test-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issues")
	req.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()

	s.handleGitHubWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "accepted" {
		t.Errorf("expected status accepted, got %q", resp["status"])
	}
	if resp["job_id"] == "" {
		t.Error("expected non-empty job_id")
	}
}

func TestWebhook_IssueOpened_WithoutForgeLabel(t *testing.T) {
	s := newTestServer("test-secret")

	payload := map[string]interface{}{
		"action": "opened",
		"issue": map[string]interface{}{
			"number": 42,
			"labels": []interface{}{
				map[string]interface{}{"name": "bug"},
			},
		},
		"repository": map[string]interface{}{
			"full_name": "org/repo",
		},
	}
	body, _ := json.Marshal(payload)
	sig := signPayload(body, "test-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issues")
	req.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()

	s.handleGitHubWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ignored" {
		t.Errorf("expected status ignored, got %q", resp["status"])
	}
}

func TestWebhook_PROpened(t *testing.T) {
	s := newTestServer("test-secret")

	payload := map[string]interface{}{
		"action": "opened",
		"pull_request": map[string]interface{}{
			"number": 10,
			"title":  "Test PR",
		},
		"repository": map[string]interface{}{
			"full_name": "org/repo",
		},
	}
	body, _ := json.Marshal(payload)
	sig := signPayload(body, "test-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()

	s.handleGitHubWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "accepted" {
		t.Errorf("expected status accepted, got %q", resp["status"])
	}
}

func TestWebhook_IssueComment_ForgeBuild(t *testing.T) {
	s := newTestServer("test-secret")

	payload := map[string]interface{}{
		"action": "created",
		"comment": map[string]interface{}{
			"body": "/forge build",
		},
		"issue": map[string]interface{}{
			"number": 99,
		},
		"repository": map[string]interface{}{
			"full_name": "org/repo",
		},
	}
	body, _ := json.Marshal(payload)
	sig := signPayload(body, "test-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	req.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()

	s.handleGitHubWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "accepted" {
		t.Errorf("expected status accepted, got %q", resp["status"])
	}
}

func TestWebhook_IssueComment_NotForgeCommand(t *testing.T) {
	s := newTestServer("test-secret")

	payload := map[string]interface{}{
		"action": "created",
		"comment": map[string]interface{}{
			"body": "LGTM",
		},
		"issue": map[string]interface{}{
			"number": 99,
		},
		"repository": map[string]interface{}{
			"full_name": "org/repo",
		},
	}
	body, _ := json.Marshal(payload)
	sig := signPayload(body, "test-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	req.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()

	s.handleGitHubWebhook(w, req)

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ignored" {
		t.Errorf("expected status ignored, got %q", resp["status"])
	}
}

func TestWebhook_MissingSecret(t *testing.T) {
	// Server with no secret configured.
	s := newTestServer("")
	s.config.Webhooks.GitHub = nil

	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "ping")
	req.Header.Set("X-Hub-Signature-256", "sha256=abc")
	w := httptest.NewRecorder()

	s.handleGitHubWebhook(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestVerifyGitHubSignature(t *testing.T) {
	tests := []struct {
		name     string
		payload  string
		secret   string
		sigFunc  func([]byte, string) string
		valid    bool
	}{
		{
			name:    "valid signature",
			payload: `{"test": true}`,
			secret:  "mysecret",
			sigFunc: signPayload,
			valid:   true,
		},
		{
			name:    "wrong secret",
			payload: `{"test": true}`,
			secret:  "mysecret",
			sigFunc: func(b []byte, _ string) string { return signPayload(b, "wrongsecret") },
			valid:   false,
		},
		{
			name:    "missing prefix",
			payload: `{"test": true}`,
			secret:  "mysecret",
			sigFunc: func([]byte, string) string { return "abcdef" },
			valid:   false,
		},
		{
			name:    "invalid hex",
			payload: `{"test": true}`,
			secret:  "mysecret",
			sigFunc: func([]byte, string) string { return "sha256=zzzz" },
			valid:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := []byte(tt.payload)
			sig := tt.sigFunc(payload, tt.secret)
			got := verifyGitHubSignature(payload, sig, tt.secret)
			if got != tt.valid {
				t.Errorf("verifyGitHubSignature() = %v, want %v", got, tt.valid)
			}
		})
	}
}

func TestHasLabel(t *testing.T) {
	issue := map[string]interface{}{
		"labels": []interface{}{
			map[string]interface{}{"name": "forge"},
			map[string]interface{}{"name": "bug"},
		},
	}

	if !hasLabel(issue, "forge") {
		t.Error("expected to find 'forge' label")
	}
	if !hasLabel(issue, "bug") {
		t.Error("expected to find 'bug' label")
	}
	if hasLabel(issue, "enhancement") {
		t.Error("did not expect to find 'enhancement' label")
	}
}

func TestBuildIssueRef(t *testing.T) {
	payload := map[string]interface{}{
		"repository": map[string]interface{}{
			"full_name": "org/repo",
		},
		"issue": map[string]interface{}{
			"number": float64(42),
		},
	}

	ref := buildIssueRef(payload)
	if ref != "gh:org/repo#42" {
		t.Errorf("expected %q, got %q", "gh:org/repo#42", ref)
	}
}

func TestBuildIssueRef_MissingFields(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]interface{}
	}{
		{"no repository", map[string]interface{}{"issue": map[string]interface{}{"number": float64(1)}}},
		{"no issue", map[string]interface{}{"repository": map[string]interface{}{"full_name": "org/repo"}}},
		{"no number", map[string]interface{}{
			"repository": map[string]interface{}{"full_name": "org/repo"},
			"issue":      map[string]interface{}{"title": "test"},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := buildIssueRef(tt.payload)
			if ref != "" {
				t.Errorf("expected empty ref, got %q", ref)
			}
		})
	}
}
