package server

import (
	"bytes"
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

func newTestServerWithLinear(secret string, triggers []config.WebhookTrigger) *Server {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.ServerConfig{
		Port: 8080,
		Webhooks: config.WebhookConfig{
			Linear: &config.LinearWebhookConfig{
				SigningSecret: secret,
				Triggers:      triggers,
			},
		},
	}
	eng, _ := engine.New(&engine.EngineConfig{}, map[string]agent.Agent{"mock": &mockWebhookAgent{}}, map[string]tracker.Tracker{}, nil)

	broker := NewSSEBroker()
	queue := NewJobQueue(broker)

	return &Server{
		engine:  eng,
		config:  cfg,
		logger:  logger,
		jobs:    queue,
		broker:  broker,
		limiter: newRateLimiter(1000, 60_000_000_000),
	}
}

func signLinearPayload(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestLinearWebhook_MissingSignature(t *testing.T) {
	s := newTestServerWithLinear("test-secret", nil)
	body := []byte(`{"action":"update","type":"Issue"}`)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/linear", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleLinearWebhook(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}

	var resp APIError
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != "MISSING_SIGNATURE" {
		t.Errorf("expected code MISSING_SIGNATURE, got %q", resp.Code)
	}
}

func TestLinearWebhook_InvalidSignature(t *testing.T) {
	s := newTestServerWithLinear("test-secret", nil)
	body := []byte(`{"action":"update","type":"Issue"}`)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/linear", bytes.NewReader(body))
	req.Header.Set("Linear-Signature", "0000000000000000000000000000000000000000000000000000000000000000")
	w := httptest.NewRecorder()

	s.handleLinearWebhook(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}

	var resp APIError
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != "INVALID_SIGNATURE" {
		t.Errorf("expected code INVALID_SIGNATURE, got %q", resp.Code)
	}
}

func TestLinearWebhook_NotConfigured(t *testing.T) {
	s := newTestServerWithLinear("", nil)
	s.config.Webhooks.Linear = nil

	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/linear", bytes.NewReader(body))
	req.Header.Set("Linear-Signature", "abc")
	w := httptest.NewRecorder()

	s.handleLinearWebhook(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestLinearWebhook_ValidPayload_MatchingLabelTrigger(t *testing.T) {
	triggers := []config.WebhookTrigger{
		{
			Event:  "Issue.update",
			Label:  "forge",
			Action: "build",
		},
	}
	s := newTestServerWithLinear("test-secret", triggers)

	payload := map[string]interface{}{
		"action": "update",
		"type":   "Issue",
		"data": map[string]interface{}{
			"id":         "uuid-123",
			"identifier": "TEAM-123",
			"title":      "Test issue",
			"labels": []interface{}{
				map[string]interface{}{"name": "forge"},
			},
			"state": map[string]interface{}{"name": "In Progress"},
		},
		"url": "https://linear.app/team/issue/TEAM-123",
	}
	body, _ := json.Marshal(payload)
	sig := signLinearPayload(body, "test-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/linear", bytes.NewReader(body))
	req.Header.Set("Linear-Signature", sig)
	w := httptest.NewRecorder()

	s.handleLinearWebhook(w, req)

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

func TestLinearWebhook_ValidPayload_NoMatchingLabel(t *testing.T) {
	triggers := []config.WebhookTrigger{
		{
			Event:  "Issue.update",
			Label:  "forge",
			Action: "build",
		},
	}
	s := newTestServerWithLinear("test-secret", triggers)

	payload := map[string]interface{}{
		"action": "update",
		"type":   "Issue",
		"data": map[string]interface{}{
			"id":         "uuid-456",
			"identifier": "TEAM-456",
			"title":      "No forge label",
			"labels": []interface{}{
				map[string]interface{}{"name": "bug"},
			},
		},
	}
	body, _ := json.Marshal(payload)
	sig := signLinearPayload(body, "test-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/linear", bytes.NewReader(body))
	req.Header.Set("Linear-Signature", sig)
	w := httptest.NewRecorder()

	s.handleLinearWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ignored" {
		t.Errorf("expected status ignored, got %q", resp["status"])
	}
}

func TestLinearWebhook_EventOnlyTrigger(t *testing.T) {
	triggers := []config.WebhookTrigger{
		{
			Event:  "Issue.create",
			Action: "build",
		},
	}
	s := newTestServerWithLinear("test-secret", triggers)

	payload := map[string]interface{}{
		"action": "create",
		"type":   "Issue",
		"data": map[string]interface{}{
			"id":         "uuid-789",
			"identifier": "TEAM-789",
			"title":      "New issue",
			"labels":     []interface{}{},
		},
	}
	body, _ := json.Marshal(payload)
	sig := signLinearPayload(body, "test-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/linear", bytes.NewReader(body))
	req.Header.Set("Linear-Signature", sig)
	w := httptest.NewRecorder()

	s.handleLinearWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "accepted" {
		t.Errorf("expected status accepted, got %q", resp["status"])
	}
}

func TestLinearWebhook_NonMatchingEvent(t *testing.T) {
	triggers := []config.WebhookTrigger{
		{
			Event:  "Issue.create",
			Action: "build",
		},
	}
	s := newTestServerWithLinear("test-secret", triggers)

	payload := map[string]interface{}{
		"action": "update",
		"type":   "Issue",
		"data": map[string]interface{}{
			"id":         "uuid-000",
			"identifier": "TEAM-000",
			"title":      "Updated issue",
			"labels":     []interface{}{},
		},
	}
	body, _ := json.Marshal(payload)
	sig := signLinearPayload(body, "test-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/linear", bytes.NewReader(body))
	req.Header.Set("Linear-Signature", sig)
	w := httptest.NewRecorder()

	s.handleLinearWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ignored" {
		t.Errorf("expected status ignored, got %q", resp["status"])
	}
}

func TestVerifyLinearSignature(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		secret  string
		sigFunc func([]byte, string) string
		valid   bool
	}{
		{
			name:    "valid signature",
			payload: `{"test": true}`,
			secret:  "mysecret",
			sigFunc: signLinearPayload,
			valid:   true,
		},
		{
			name:    "wrong secret",
			payload: `{"test": true}`,
			secret:  "mysecret",
			sigFunc: func(b []byte, _ string) string { return signLinearPayload(b, "wrongsecret") },
			valid:   false,
		},
		{
			name:    "invalid hex",
			payload: `{"test": true}`,
			secret:  "mysecret",
			sigFunc: func([]byte, string) string { return "zzzz" },
			valid:   false,
		},
		{
			name:    "empty signature",
			payload: `{"test": true}`,
			secret:  "mysecret",
			sigFunc: func([]byte, string) string { return "" },
			valid:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := []byte(tt.payload)
			sig := tt.sigFunc(payload, tt.secret)
			got := verifyLinearSignature(payload, sig, tt.secret)
			if got != tt.valid {
				t.Errorf("verifyLinearSignature() = %v, want %v", got, tt.valid)
			}
		})
	}
}

func TestLinearLabels(t *testing.T) {
	tests := []struct {
		name     string
		payload  map[string]interface{}
		expected []string
	}{
		{
			"with labels",
			map[string]interface{}{
				"data": map[string]interface{}{
					"labels": []interface{}{
						map[string]interface{}{"name": "forge"},
						map[string]interface{}{"name": "bug"},
					},
				},
			},
			[]string{"forge", "bug"},
		},
		{
			"no data",
			map[string]interface{}{},
			nil,
		},
		{
			"empty labels",
			map[string]interface{}{
				"data": map[string]interface{}{
					"labels": []interface{}{},
				},
			},
			[]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := linearLabels(tt.payload)
			if tt.expected == nil && got != nil {
				t.Errorf("expected nil, got %v", got)
				return
			}
			if len(got) != len(tt.expected) {
				t.Errorf("expected %d labels, got %d", len(tt.expected), len(got))
				return
			}
			for i, v := range tt.expected {
				if got[i] != v {
					t.Errorf("label[%d] = %q, want %q", i, got[i], v)
				}
			}
		})
	}
}

func TestLinearIssueIdentifier(t *testing.T) {
	tests := []struct {
		name     string
		payload  map[string]interface{}
		expected string
	}{
		{
			"valid identifier",
			map[string]interface{}{
				"data": map[string]interface{}{"identifier": "TEAM-123"},
			},
			"TEAM-123",
		},
		{
			"no data",
			map[string]interface{}{},
			"",
		},
		{
			"no identifier",
			map[string]interface{}{
				"data": map[string]interface{}{"id": "uuid"},
			},
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := linearIssueIdentifier(tt.payload)
			if got != tt.expected {
				t.Errorf("linearIssueIdentifier() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestLinearMatchTrigger(t *testing.T) {
	tests := []struct {
		name     string
		trigger  config.WebhookTrigger
		event    string
		labels   []string
		expected bool
	}{
		{
			"event matches",
			config.WebhookTrigger{Event: "Issue.update"},
			"Issue.update", nil,
			true,
		},
		{
			"event does not match",
			config.WebhookTrigger{Event: "Issue.create"},
			"Issue.update", nil,
			false,
		},
		{
			"label matches",
			config.WebhookTrigger{Event: "Issue.update", Label: "forge"},
			"Issue.update", []string{"forge", "bug"},
			true,
		},
		{
			"label does not match",
			config.WebhookTrigger{Event: "Issue.update", Label: "forge"},
			"Issue.update", []string{"bug"},
			false,
		},
		{
			"label case insensitive",
			config.WebhookTrigger{Event: "Issue.update", Label: "Forge"},
			"Issue.update", []string{"forge"},
			true,
		},
		{
			"empty trigger matches everything",
			config.WebhookTrigger{},
			"Issue.update", []string{"forge"},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := linearMatchTrigger(tt.trigger, tt.event, tt.labels)
			if got != tt.expected {
				t.Errorf("linearMatchTrigger() = %v, want %v", got, tt.expected)
			}
		})
	}
}
