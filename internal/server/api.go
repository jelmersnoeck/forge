package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jelmersnoeck/forge/internal/engine"
)

// --- Request / Response types ---

// BuildAPIRequest is the JSON body for POST /api/v1/build.
type BuildAPIRequest struct {
	Issue         string   `json:"issue"`
	PrincipleSets []string `json:"principles,omitempty"`
	WorkDir       string   `json:"work_dir,omitempty"`
	BaseBranch    string   `json:"base_branch,omitempty"`
}

// ReviewAPIRequest is the JSON body for POST /api/v1/review.
type ReviewAPIRequest struct {
	Diff          string   `json:"diff"`
	PrincipleSets []string `json:"principles,omitempty"`
	WorkDir       string   `json:"work_dir,omitempty"`
}

// PlanAPIRequest is the JSON body for POST /api/v1/plan.
type PlanAPIRequest struct {
	Issue         string   `json:"issue"`
	PrincipleSets []string `json:"principles,omitempty"`
	WorkDir       string   `json:"work_dir,omitempty"`
}

// APIError is a structured error response.
type APIError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// --- Handlers ---

// validateWorkDir validates and resolves a work_dir from an API request.
// An empty value defaults to the current working directory. The path must be
// absolute, must exist as a directory, and must not contain ".." components
// after cleaning.
func validateWorkDir(workDir string) (string, error) {
	if workDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("validate work_dir: %w", err)
		}
		return cwd, nil
	}

	if !filepath.IsAbs(workDir) {
		return "", fmt.Errorf("validate work_dir: path must be absolute")
	}

	// Reject paths containing ".." components before cleaning, which could be
	// used to traverse outside an intended directory boundary.
	if containsDotDot(workDir) {
		return "", fmt.Errorf("validate work_dir: path must not contain '..' components")
	}

	cleaned := filepath.Clean(workDir)

	info, err := os.Stat(cleaned)
	if err != nil {
		return "", fmt.Errorf("validate work_dir: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("validate work_dir: path is not a directory")
	}

	return cleaned, nil
}

// handleBuild accepts a build request and submits it as an async job.
func (s *Server) handleBuild(w http.ResponseWriter, r *http.Request) {
	var req BuildAPIRequest
	if err := s.readJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_JSON", "failed to parse request body")
		return
	}

	if req.Issue == "" {
		s.writeError(w, http.StatusBadRequest, "MISSING_FIELD", "issue is required")
		return
	}

	workDir, err := validateWorkDir(req.WorkDir)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_WORK_DIR", err.Error())
		return
	}
	req.WorkDir = workDir

	job := &Job{
		Type:    JobTypeBuild,
		Request: req,
	}
	jobID := s.jobs.Submit(job)

	s.logger.Info("build job submitted", "job_id", jobID, "issue", req.Issue)
	s.writeJSON(w, http.StatusAccepted, map[string]string{
		"job_id": jobID,
		"status": string(JobStatusPending),
	})
}

// handleReview runs a synchronous review and returns findings.
func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	var req ReviewAPIRequest
	if err := s.readJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_JSON", "failed to parse request body")
		return
	}

	if req.Diff == "" {
		s.writeError(w, http.StatusBadRequest, "MISSING_FIELD", "diff is required")
		return
	}

	workDir, err := validateWorkDir(req.WorkDir)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_WORK_DIR", err.Error())
		return
	}

	result, err := s.engine.Review(r.Context(), engine.ReviewRequest{
		Diff:          req.Diff,
		PrincipleSets: req.PrincipleSets,
		WorkDir:       workDir,
	})
	if err != nil {
		s.logger.Error("review failed", "error", err)
		s.writeError(w, http.StatusInternalServerError, "REVIEW_FAILED", "review failed")
		return
	}

	s.writeJSON(w, http.StatusOK, result)
}

// handlePlan runs a synchronous plan generation and returns the plan.
func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	var req PlanAPIRequest
	if err := s.readJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_JSON", "failed to parse request body")
		return
	}

	if req.Issue == "" {
		s.writeError(w, http.StatusBadRequest, "MISSING_FIELD", "issue is required")
		return
	}

	workDir, err := validateWorkDir(req.WorkDir)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_WORK_DIR", err.Error())
		return
	}

	result, err := s.engine.Plan(r.Context(), engine.PlanRequest{
		IssueRef:      req.Issue,
		PrincipleSets: req.PrincipleSets,
		WorkDir:       workDir,
	})
	if err != nil {
		s.logger.Error("plan failed", "error", err)
		s.writeError(w, http.StatusInternalServerError, "PLAN_FAILED", "plan generation failed")
		return
	}

	s.writeJSON(w, http.StatusOK, result)
}

// handleGetJob returns a single job by ID.
func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	jobID := extractPathParam(r.URL.Path, "/api/v1/jobs/")
	// Strip trailing /stream if present (routed separately).
	jobID = strings.TrimSuffix(jobID, "/stream")

	if jobID == "" {
		s.writeError(w, http.StatusBadRequest, "MISSING_ID", "job ID is required")
		return
	}

	job, ok := s.jobs.Get(jobID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "job not found")
		return
	}

	s.writeJSON(w, http.StatusOK, job)
}

// handleListJobs returns a paginated list of jobs.
func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 20)
	offset := queryInt(r, "offset", 0)

	if limit < 1 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	filter := JobFilter{
		Status: JobStatus(r.URL.Query().Get("status")),
		Type:   JobType(r.URL.Query().Get("type")),
		Limit:  limit,
		Offset: offset,
	}

	jobs, err := s.jobs.ListFiltered(filter)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "LIST_FAILED", "failed to list jobs")
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"jobs":   jobs,
		"limit":  limit,
		"offset": offset,
	})
}

// handleJobStream serves an SSE stream for a specific job.
func (s *Server) handleJobStream(w http.ResponseWriter, r *http.Request) {
	// Extract job ID: /api/v1/jobs/{id}/stream
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/jobs/")
	jobID := strings.TrimSuffix(path, "/stream")

	if jobID == "" {
		s.writeError(w, http.StatusBadRequest, "MISSING_ID", "job ID is required")
		return
	}

	// Verify the job exists.
	if _, ok := s.jobs.Get(jobID); !ok {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "job not found")
		return
	}

	// Check that the client supports SSE.
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "STREAMING_ERROR", "streaming not supported")
		return
	}

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering.

	// Subscribe to events for this job.
	ch := s.broker.Subscribe(jobID)
	defer s.broker.Unsubscribe(jobID, ch)

	// Send initial connected event.
	w.Write(formatSSEEvent(Event{
		Type: "connected",
		Data: map[string]string{"job_id": jobID},
	}))
	flusher.Flush()

	// Stream events until client disconnects or job completes.
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			w.Write(formatSSEEvent(event))
			flusher.Flush()

			// Close after job_completed event.
			if event.Type == "job_completed" {
				return
			}
		}
	}
}

// --- Helpers ---

// readJSON decodes the request body into v.
func (s *Server) readJSON(r *http.Request, v interface{}) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB limit
	if err != nil {
		return fmt.Errorf("reading body: %w", err)
	}
	defer r.Body.Close()
	return json.Unmarshal(body, v)
}

// writeJSON writes a JSON response with the given status code.
func (s *Server) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	// Only call WriteHeader if status is not 0 (already set by caller).
	if status > 0 {
		w.WriteHeader(status)
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Error("failed to write JSON response", "error", err)
	}
}

// writeError writes a structured error response.
func (s *Server) writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(APIError{
		Error: message,
		Code:  code,
	})
}

// extractPathParam extracts the remaining path after a prefix.
func extractPathParam(path, prefix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	param := strings.TrimPrefix(path, prefix)
	// Remove any trailing slashes or sub-paths.
	if idx := strings.Index(param, "/"); idx != -1 {
		param = param[:idx]
	}
	return param
}

// containsDotDot reports whether the path contains a ".." component.
func containsDotDot(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// queryInt reads an integer query parameter with a default.
func queryInt(r *http.Request, key string, defaultVal int) int {
	s := r.URL.Query().Get(key)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return v
}
