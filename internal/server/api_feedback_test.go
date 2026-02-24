package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jelmersnoeck/forge/internal/engine"
)

func TestHandleFeedback_Success(t *testing.T) {
	s := newFullTestServer()

	body, _ := json.Marshal(FeedbackAPIRequest{
		PRNumber:     42,
		RepoFullName: "org/repo",
		ReviewBody:   "Please fix error handling",
		Comments: []engine.ReviewComment{
			{
				Path: "main.go",
				Line: 10,
				Body: "Missing nil check",
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feedback", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleFeedback(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["job_id"] == "" {
		t.Error("expected non-empty job_id")
	}
	if resp["status"] != string(JobStatusPending) {
		t.Errorf("expected status %q, got %q", JobStatusPending, resp["status"])
	}
}

func TestHandleFeedback_MissingPRNumber(t *testing.T) {
	s := newFullTestServer()

	body, _ := json.Marshal(FeedbackAPIRequest{
		RepoFullName: "org/repo",
		ReviewBody:   "Fix it",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feedback", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleFeedback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	var resp APIError
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != "MISSING_FIELD" {
		t.Errorf("expected code MISSING_FIELD, got %q", resp.Code)
	}
}

func TestHandleFeedback_MissingRepo(t *testing.T) {
	s := newFullTestServer()

	body, _ := json.Marshal(FeedbackAPIRequest{
		PRNumber:   42,
		ReviewBody: "Fix it",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feedback", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleFeedback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	var resp APIError
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != "MISSING_FIELD" {
		t.Errorf("expected code MISSING_FIELD, got %q", resp.Code)
	}
}

func TestHandleFeedback_InvalidJSON(t *testing.T) {
	s := newFullTestServer()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/feedback", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()

	s.handleFeedback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	var resp APIError
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != "INVALID_JSON" {
		t.Errorf("expected code INVALID_JSON, got %q", resp.Code)
	}
}

func TestHandleFeedback_InvalidWorkDir(t *testing.T) {
	s := newFullTestServer()

	body, _ := json.Marshal(FeedbackAPIRequest{
		PRNumber:     42,
		RepoFullName: "org/repo",
		ReviewBody:   "Fix it",
		WorkDir:      "relative/path",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feedback", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleFeedback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	var resp APIError
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != "INVALID_WORK_DIR" {
		t.Errorf("expected code INVALID_WORK_DIR, got %q", resp.Code)
	}
}

func TestHandleFeedback_EmptyComments(t *testing.T) {
	s := newFullTestServer()

	body, _ := json.Marshal(FeedbackAPIRequest{
		PRNumber:     42,
		RepoFullName: "org/repo",
		ReviewBody:   "Overall looks good but needs polish",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feedback", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleFeedback(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleFeedback_ViaRoutes(t *testing.T) {
	s := newFullTestServer()
	handler := s.routes()

	body, _ := json.Marshal(FeedbackAPIRequest{
		PRNumber:     42,
		RepoFullName: "org/repo",
		ReviewBody:   "Fix everything",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feedback", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
}
