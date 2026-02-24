package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebhook_PRReview_ChangesRequested(t *testing.T) {
	s := newTestServer("test-secret")

	payload := map[string]interface{}{
		"action": "submitted",
		"review": map[string]interface{}{
			"state": "changes_requested",
			"body":  "Please fix the error handling",
		},
		"pull_request": map[string]interface{}{
			"number": 42,
			"title":  "Add feature",
		},
		"repository": map[string]interface{}{
			"full_name": "org/repo",
		},
	}
	body, _ := json.Marshal(payload)
	sig := signPayload(body, "test-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request_review")
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

func TestWebhook_PRReview_Approved(t *testing.T) {
	s := newTestServer("test-secret")

	payload := map[string]interface{}{
		"action": "submitted",
		"review": map[string]interface{}{
			"state": "approved",
			"body":  "LGTM",
		},
		"pull_request": map[string]interface{}{
			"number": 42,
		},
		"repository": map[string]interface{}{
			"full_name": "org/repo",
		},
	}
	body, _ := json.Marshal(payload)
	sig := signPayload(body, "test-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request_review")
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

func TestWebhook_PRReview_Commented(t *testing.T) {
	s := newTestServer("test-secret")

	payload := map[string]interface{}{
		"action": "submitted",
		"review": map[string]interface{}{
			"state": "commented",
			"body":  "Just a comment",
		},
		"pull_request": map[string]interface{}{
			"number": 42,
		},
		"repository": map[string]interface{}{
			"full_name": "org/repo",
		},
	}
	body, _ := json.Marshal(payload)
	sig := signPayload(body, "test-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request_review")
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

func TestWebhook_PRReview_NoReviewObject(t *testing.T) {
	s := newTestServer("test-secret")

	payload := map[string]interface{}{
		"action": "submitted",
		"pull_request": map[string]interface{}{
			"number": 42,
		},
		"repository": map[string]interface{}{
			"full_name": "org/repo",
		},
	}
	body, _ := json.Marshal(payload)
	sig := signPayload(body, "test-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request_review")
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

func TestWebhook_PRReview_NoPullRequest(t *testing.T) {
	s := newTestServer("test-secret")

	payload := map[string]interface{}{
		"action": "submitted",
		"review": map[string]interface{}{
			"state": "changes_requested",
			"body":  "Fix it",
		},
		"repository": map[string]interface{}{
			"full_name": "org/repo",
		},
	}
	body, _ := json.Marshal(payload)
	sig := signPayload(body, "test-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request_review")
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

func TestWebhook_PRReview_MissingRepo(t *testing.T) {
	s := newTestServer("test-secret")

	payload := map[string]interface{}{
		"action": "submitted",
		"review": map[string]interface{}{
			"state": "changes_requested",
			"body":  "Fix it",
		},
		"pull_request": map[string]interface{}{
			"number": 42,
		},
		"repository": map[string]interface{}{},
	}
	body, _ := json.Marshal(payload)
	sig := signPayload(body, "test-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request_review")
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

func TestWebhook_PRReview_ReviewBodyExtracted(t *testing.T) {
	s := newTestServer("test-secret")

	reviewBody := "This code has several issues:\n1. Missing error handling\n2. No tests"

	payload := map[string]interface{}{
		"action": "submitted",
		"review": map[string]interface{}{
			"state": "changes_requested",
			"body":  reviewBody,
		},
		"pull_request": map[string]interface{}{
			"number": 10,
		},
		"repository": map[string]interface{}{
			"full_name": "acme/app",
		},
	}
	body, _ := json.Marshal(payload)
	sig := signPayload(body, "test-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request_review")
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

	// Verify the job was submitted with the correct data.
	jobID := resp["job_id"]
	job, ok := s.jobs.Get(jobID)
	if !ok {
		t.Fatalf("job %s not found", jobID)
	}
	if job.Type != JobTypeFeedback {
		t.Errorf("expected job type feedback, got %s", job.Type)
	}

	// Verify request contains review body.
	reqMap, ok := job.Request.(map[string]interface{})
	if !ok {
		t.Fatalf("expected request to be map[string]interface{}, got %T", job.Request)
	}
	if reqMap["review_body"] != reviewBody {
		t.Errorf("expected review_body %q, got %q", reviewBody, reqMap["review_body"])
	}
}
