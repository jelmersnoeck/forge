package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleBuild_MissingIssue(t *testing.T) {
	s := newFullTestServer()

	body, _ := json.Marshal(map[string]string{"issue": ""})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/build", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleBuild(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	var resp APIError
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != "MISSING_FIELD" {
		t.Errorf("expected code MISSING_FIELD, got %q", resp.Code)
	}
}

func TestHandleBuild_InvalidJSON(t *testing.T) {
	s := newFullTestServer()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/build", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()

	s.handleBuild(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleBuild_Success(t *testing.T) {
	s := newFullTestServer()

	body, _ := json.Marshal(BuildAPIRequest{
		Issue:         "gh:org/repo#123",
		PrincipleSets: []string{"security"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/build", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleBuild(w, req)

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

func TestHandleReview_MissingDiff(t *testing.T) {
	s := newFullTestServer()

	body, _ := json.Marshal(ReviewAPIRequest{Diff: ""})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/review", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleReview(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandlePlan_MissingIssue(t *testing.T) {
	s := newFullTestServer()

	body, _ := json.Marshal(PlanAPIRequest{Issue: ""})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/plan", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handlePlan(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleListJobs_Empty(t *testing.T) {
	s := newFullTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	w := httptest.NewRecorder()

	s.handleListJobs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	// Jobs should be null or empty array.
	jobs := resp["jobs"]
	if jobs != nil {
		if arr, ok := jobs.([]interface{}); ok && len(arr) != 0 {
			t.Errorf("expected empty jobs list, got %v", arr)
		}
	}
}

func TestHandleListJobs_WithPagination(t *testing.T) {
	s := newFullTestServer()

	// Submit some jobs.
	for i := 0; i < 5; i++ {
		s.jobs.Submit(&Job{Type: JobTypeBuild, Request: i})
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs?limit=2&offset=0", nil)
	w := httptest.NewRecorder()

	s.handleListJobs(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	jobs := resp["jobs"].([]interface{})
	if len(jobs) != 2 {
		t.Errorf("expected 2 jobs, got %d", len(jobs))
	}
}

func TestHandleGetJob_NotFound(t *testing.T) {
	s := newFullTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/nonexistent", nil)
	w := httptest.NewRecorder()

	s.handleGetJob(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleGetJob_Found(t *testing.T) {
	s := newFullTestServer()

	jobID := s.jobs.Submit(&Job{Type: JobTypeBuild, Request: "test"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID, nil)
	w := httptest.NewRecorder()

	s.handleGetJob(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp Job
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.ID != jobID {
		t.Errorf("expected job ID %q, got %q", jobID, resp.ID)
	}
}

func TestExtractPathParam(t *testing.T) {
	tests := []struct {
		path   string
		prefix string
		want   string
	}{
		{"/api/v1/jobs/abc123", "/api/v1/jobs/", "abc123"},
		{"/api/v1/jobs/abc123/stream", "/api/v1/jobs/", "abc123"},
		{"/api/v1/jobs/", "/api/v1/jobs/", ""},
		{"/other/path", "/api/v1/jobs/", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := extractPathParam(tt.path, tt.prefix)
			if got != tt.want {
				t.Errorf("extractPathParam(%q, %q) = %q, want %q", tt.path, tt.prefix, got, tt.want)
			}
		})
	}
}

func TestQueryInt(t *testing.T) {
	tests := []struct {
		url      string
		key      string
		fallback int
		want     int
	}{
		{"/test?limit=10", "limit", 20, 10},
		{"/test?limit=abc", "limit", 20, 20},
		{"/test", "limit", 20, 20},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tt.url, nil)
			got := queryInt(r, tt.key, tt.fallback)
			if got != tt.want {
				t.Errorf("queryInt() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRoutes_FullIntegration(t *testing.T) {
	s := newFullTestServer()
	handler := s.routes()

	// Test POST /api/v1/build via the full handler chain.
	body, _ := json.Marshal(BuildAPIRequest{Issue: "gh:org/repo#1"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/build", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	// Verify request ID middleware ran.
	if w.Header().Get("X-Request-ID") == "" {
		t.Error("expected X-Request-ID header from middleware")
	}

	// Verify CORS middleware ran for allowed origin.
	if w.Header().Get("Access-Control-Allow-Origin") != "https://example.com" {
		t.Errorf("expected CORS header for allowed origin, got %q", w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestValidateWorkDir_EmptyDefaultsToCwd(t *testing.T) {
	dir, err := validateWorkDir("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cwd, _ := os.Getwd()
	if dir != cwd {
		t.Errorf("expected %q, got %q", cwd, dir)
	}
}

func TestValidateWorkDir_ValidAbsoluteDir(t *testing.T) {
	tmp := t.TempDir()
	dir, err := validateWorkDir(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != filepath.Clean(tmp) {
		t.Errorf("expected %q, got %q", filepath.Clean(tmp), dir)
	}
}

func TestValidateWorkDir_RelativePath(t *testing.T) {
	_, err := validateWorkDir("relative/path")
	if err == nil {
		t.Fatal("expected error for relative path, got nil")
	}
	if !strings.Contains(err.Error(), "must be absolute") {
		t.Errorf("expected 'must be absolute' in error, got: %v", err)
	}
}

func TestValidateWorkDir_DotDotTraversal(t *testing.T) {
	_, err := validateWorkDir("/tmp/../../../etc")
	if err == nil {
		t.Fatal("expected error for .. traversal, got nil")
	}
}

func TestValidateWorkDir_NonExistent(t *testing.T) {
	_, err := validateWorkDir("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for non-existent path, got nil")
	}
}

func TestValidateWorkDir_NotADirectory(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "file.txt")
	os.WriteFile(f, []byte("hello"), 0o644)

	_, err := validateWorkDir(f)
	if err == nil {
		t.Fatal("expected error for file path, got nil")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("expected 'not a directory' in error, got: %v", err)
	}
}

func TestHandleBuild_InvalidWorkDir(t *testing.T) {
	s := newFullTestServer()

	body, _ := json.Marshal(BuildAPIRequest{
		Issue:   "gh:org/repo#1",
		WorkDir: "relative/path",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/build", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleBuild(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	var resp APIError
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != "INVALID_WORK_DIR" {
		t.Errorf("expected code INVALID_WORK_DIR, got %q", resp.Code)
	}
}

func TestHandleReview_InvalidWorkDir(t *testing.T) {
	s := newFullTestServer()

	body, _ := json.Marshal(ReviewAPIRequest{
		Diff:    "some diff content",
		WorkDir: "relative/path",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/review", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleReview(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	var resp APIError
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != "INVALID_WORK_DIR" {
		t.Errorf("expected code INVALID_WORK_DIR, got %q", resp.Code)
	}
}

func TestHandlePlan_InvalidWorkDir(t *testing.T) {
	s := newFullTestServer()

	body, _ := json.Marshal(PlanAPIRequest{
		Issue:   "gh:org/repo#1",
		WorkDir: "relative/path",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/plan", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handlePlan(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	var resp APIError
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != "INVALID_WORK_DIR" {
		t.Errorf("expected code INVALID_WORK_DIR, got %q", resp.Code)
	}
}
