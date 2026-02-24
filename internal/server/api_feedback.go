package server

import (
	"net/http"

	"github.com/jelmersnoeck/forge/internal/engine"
)

// FeedbackAPIRequest is the JSON body for POST /api/v1/feedback.
type FeedbackAPIRequest struct {
	PRNumber      int                    `json:"pr_number"`
	RepoFullName  string                 `json:"repo_full_name"`
	ReviewBody    string                 `json:"review_body,omitempty"`
	Comments      []engine.ReviewComment `json:"comments"`
	PrincipleSets []string               `json:"principles,omitempty"`
	WorkDir       string                 `json:"work_dir,omitempty"`
}

// handleFeedback accepts a feedback request and submits it as an async job.
func (s *Server) handleFeedback(w http.ResponseWriter, r *http.Request) {
	var req FeedbackAPIRequest
	if err := s.readJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_JSON", "failed to parse request body")
		return
	}

	if req.PRNumber == 0 {
		s.writeError(w, http.StatusBadRequest, "MISSING_FIELD", "pr_number is required")
		return
	}

	if req.RepoFullName == "" {
		s.writeError(w, http.StatusBadRequest, "MISSING_FIELD", "repo_full_name is required")
		return
	}

	workDir, err := validateWorkDir(req.WorkDir)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_WORK_DIR", err.Error())
		return
	}
	req.WorkDir = workDir

	job := &Job{
		Type:    JobTypeFeedback,
		Request: req,
	}
	jobID := s.jobs.Submit(job)

	s.logger.Info("feedback job submitted",
		"job_id", jobID,
		"pr_number", req.PRNumber,
		"repo", req.RepoFullName,
	)

	s.writeJSON(w, http.StatusAccepted, map[string]string{
		"job_id": jobID,
		"status": string(JobStatusPending),
	})
}
