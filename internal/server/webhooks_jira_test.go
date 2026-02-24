package server

import (
	"bytes"
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

func newTestServerWithJira(auth string, triggers []config.WebhookTrigger) *Server {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.ServerConfig{
		Port: 8080,
		Webhooks: config.WebhookConfig{
			Jira: &config.JiraWebhookConfig{
				Auth:     auth,
				Triggers: triggers,
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

func TestJiraWebhook_MissingAuth(t *testing.T) {
	s := newTestServerWithJira("test-token", nil)
	body := []byte(`{"webhookEvent":"jira:issue_updated"}`)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/jira", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleJiraWebhook(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}

	var resp APIError
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != "MISSING_AUTH" {
		t.Errorf("expected code MISSING_AUTH, got %q", resp.Code)
	}
}

func TestJiraWebhook_InvalidAuth(t *testing.T) {
	s := newTestServerWithJira("test-token", nil)
	body := []byte(`{"webhookEvent":"jira:issue_updated"}`)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/jira", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()

	s.handleJiraWebhook(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}

	var resp APIError
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != "INVALID_AUTH" {
		t.Errorf("expected code INVALID_AUTH, got %q", resp.Code)
	}
}

func TestJiraWebhook_NotConfigured(t *testing.T) {
	s := newTestServerWithJira("", nil)
	s.config.Webhooks.Jira = nil

	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/jira", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer any")
	w := httptest.NewRecorder()

	s.handleJiraWebhook(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestJiraWebhook_StatusTransition_MatchingTrigger(t *testing.T) {
	triggers := []config.WebhookTrigger{
		{
			Event:        "jira:issue_updated",
			TransitionTo: "Ready for Forge",
			Action:       "build",
		},
	}
	s := newTestServerWithJira("test-token", triggers)

	payload := map[string]interface{}{
		"webhookEvent": "jira:issue_updated",
		"issue": map[string]interface{}{
			"key": "PROJ-123",
			"fields": map[string]interface{}{
				"summary": "Test issue",
				"labels":  []interface{}{},
				"status":  map[string]interface{}{"name": "Ready for Forge"},
			},
		},
		"changelog": map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{
					"field":      "status",
					"fromString": "To Do",
					"toString":   "Ready for Forge",
				},
			},
		},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/jira", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	s.handleJiraWebhook(w, req)

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

func TestJiraWebhook_StatusTransition_NonMatching(t *testing.T) {
	triggers := []config.WebhookTrigger{
		{
			Event:        "jira:issue_updated",
			TransitionTo: "Ready for Forge",
			Action:       "build",
		},
	}
	s := newTestServerWithJira("test-token", triggers)

	payload := map[string]interface{}{
		"webhookEvent": "jira:issue_updated",
		"issue": map[string]interface{}{
			"key": "PROJ-123",
			"fields": map[string]interface{}{
				"summary": "Test issue",
				"labels":  []interface{}{},
				"status":  map[string]interface{}{"name": "In Progress"},
			},
		},
		"changelog": map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{
					"field":      "status",
					"fromString": "To Do",
					"toString":   "In Progress",
				},
			},
		},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/jira", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	s.handleJiraWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ignored" {
		t.Errorf("expected status ignored, got %q", resp["status"])
	}
}

func TestJiraWebhook_LabelTrigger_Matching(t *testing.T) {
	triggers := []config.WebhookTrigger{
		{
			Event:  "jira:issue_updated",
			Label:  "forge",
			Action: "build",
		},
	}
	s := newTestServerWithJira("test-token", triggers)

	payload := map[string]interface{}{
		"webhookEvent": "jira:issue_updated",
		"issue": map[string]interface{}{
			"key": "PROJ-456",
			"fields": map[string]interface{}{
				"summary": "Labeled issue",
				"labels":  []interface{}{"forge", "backend"},
			},
		},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/jira", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	s.handleJiraWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "accepted" {
		t.Errorf("expected status accepted, got %q", resp["status"])
	}
}

func TestJiraWebhook_LabelTrigger_NonMatching(t *testing.T) {
	triggers := []config.WebhookTrigger{
		{
			Event:  "jira:issue_updated",
			Label:  "forge",
			Action: "build",
		},
	}
	s := newTestServerWithJira("test-token", triggers)

	payload := map[string]interface{}{
		"webhookEvent": "jira:issue_updated",
		"issue": map[string]interface{}{
			"key": "PROJ-456",
			"fields": map[string]interface{}{
				"summary": "No forge label",
				"labels":  []interface{}{"backend"},
			},
		},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/jira", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	s.handleJiraWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ignored" {
		t.Errorf("expected status ignored, got %q", resp["status"])
	}
}

func TestJiraWebhook_TransitionTo_CaseInsensitive(t *testing.T) {
	triggers := []config.WebhookTrigger{
		{
			Event:        "jira:issue_updated",
			TransitionTo: "ready for forge",
			Action:       "build",
		},
	}
	s := newTestServerWithJira("test-token", triggers)

	payload := map[string]interface{}{
		"webhookEvent": "jira:issue_updated",
		"issue": map[string]interface{}{
			"key": "PROJ-789",
			"fields": map[string]interface{}{
				"summary": "Case test",
				"labels":  []interface{}{},
			},
		},
		"changelog": map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{
					"field":      "status",
					"fromString": "To Do",
					"toString":   "Ready For Forge",
				},
			},
		},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/jira", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	s.handleJiraWebhook(w, req)

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "accepted" {
		t.Errorf("expected status accepted, got %q", resp["status"])
	}
}

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		expected string
	}{
		{"valid bearer token", "Bearer my-secret-token", "my-secret-token"},
		{"lowercase bearer", "bearer my-secret-token", "my-secret-token"},
		{"empty header", "", ""},
		{"no bearer prefix", "Basic abc123", ""},
		{"bearer only", "Bearer ", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			got := extractBearerToken(req)
			if got != tt.expected {
				t.Errorf("extractBearerToken() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestJiraIssueKey(t *testing.T) {
	tests := []struct {
		name     string
		payload  map[string]interface{}
		expected string
	}{
		{
			"valid key",
			map[string]interface{}{"issue": map[string]interface{}{"key": "PROJ-123"}},
			"PROJ-123",
		},
		{
			"no issue",
			map[string]interface{}{},
			"",
		},
		{
			"no key",
			map[string]interface{}{"issue": map[string]interface{}{"summary": "test"}},
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := jiraIssueKey(tt.payload)
			if got != tt.expected {
				t.Errorf("jiraIssueKey() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestJiraStatusTransition(t *testing.T) {
	tests := []struct {
		name     string
		payload  map[string]interface{}
		expected string
	}{
		{
			"valid transition",
			map[string]interface{}{
				"changelog": map[string]interface{}{
					"items": []interface{}{
						map[string]interface{}{
							"field":      "status",
							"fromString": "To Do",
							"toString":   "In Progress",
						},
					},
				},
			},
			"In Progress",
		},
		{
			"no changelog",
			map[string]interface{}{},
			"",
		},
		{
			"no status change",
			map[string]interface{}{
				"changelog": map[string]interface{}{
					"items": []interface{}{
						map[string]interface{}{
							"field":      "priority",
							"fromString": "Low",
							"toString":   "High",
						},
					},
				},
			},
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := jiraStatusTransition(tt.payload)
			if got != tt.expected {
				t.Errorf("jiraStatusTransition() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestJiraMatchTrigger(t *testing.T) {
	tests := []struct {
		name      string
		trigger   config.WebhookTrigger
		event     string
		newStatus string
		labels    []string
		expected  bool
	}{
		{
			"event matches",
			config.WebhookTrigger{Event: "jira:issue_updated"},
			"jira:issue_updated", "", nil,
			true,
		},
		{
			"event does not match",
			config.WebhookTrigger{Event: "jira:issue_created"},
			"jira:issue_updated", "", nil,
			false,
		},
		{
			"transition matches",
			config.WebhookTrigger{Event: "jira:issue_updated", TransitionTo: "Done"},
			"jira:issue_updated", "Done", nil,
			true,
		},
		{
			"transition does not match",
			config.WebhookTrigger{Event: "jira:issue_updated", TransitionTo: "Done"},
			"jira:issue_updated", "In Progress", nil,
			false,
		},
		{
			"label matches",
			config.WebhookTrigger{Event: "jira:issue_updated", Label: "forge"},
			"jira:issue_updated", "", []string{"forge", "bug"},
			true,
		},
		{
			"label does not match",
			config.WebhookTrigger{Event: "jira:issue_updated", Label: "forge"},
			"jira:issue_updated", "", []string{"bug"},
			false,
		},
		{
			"all conditions match",
			config.WebhookTrigger{Event: "jira:issue_updated", TransitionTo: "Ready", Label: "forge"},
			"jira:issue_updated", "Ready", []string{"forge"},
			true,
		},
		{
			"empty trigger matches everything",
			config.WebhookTrigger{},
			"jira:issue_updated", "Done", []string{"forge"},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := jiraMatchTrigger(tt.trigger, tt.event, tt.newStatus, tt.labels)
			if got != tt.expected {
				t.Errorf("jiraMatchTrigger() = %v, want %v", got, tt.expected)
			}
		})
	}
}
