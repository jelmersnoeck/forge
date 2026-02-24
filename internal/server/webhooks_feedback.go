package server

import (
	"fmt"
	"net/http"

	"github.com/jelmersnoeck/forge/internal/engine"
)

// handlePRReview processes a pull_request_review.submitted webhook event.
// It only acts on reviews with state "changes_requested", ignoring
// "approved" and "commented" states.
func (s *Server) handlePRReview(w http.ResponseWriter, payload map[string]interface{}) {
	review, _ := payload["review"].(map[string]interface{})
	if review == nil {
		s.writeJSON(w, http.StatusOK, map[string]string{
			"status": "ignored",
			"reason": "no review in payload",
		})
		return
	}

	// Only act on "changes_requested" — ignore "approved" and "commented".
	state, _ := review["state"].(string)
	if state != "changes_requested" {
		s.writeJSON(w, http.StatusOK, map[string]string{
			"status": "ignored",
			"reason": fmt.Sprintf("review state is %q, not changes_requested", state),
		})
		return
	}

	// Extract PR number.
	pr, _ := payload["pull_request"].(map[string]interface{})
	if pr == nil {
		s.writeJSON(w, http.StatusOK, map[string]string{
			"status": "ignored",
			"reason": "no pull_request in payload",
		})
		return
	}
	prNumber, ok := pr["number"].(float64)
	if !ok {
		s.writeJSON(w, http.StatusOK, map[string]string{
			"status": "ignored",
			"reason": "missing pr number",
		})
		return
	}

	// Extract repo.
	repo, _ := payload["repository"].(map[string]interface{})
	repoFullName, _ := repo["full_name"].(string)
	if repoFullName == "" {
		s.writeJSON(w, http.StatusOK, map[string]string{
			"status": "ignored",
			"reason": "missing repository full_name",
		})
		return
	}

	// Extract review body.
	reviewBody, _ := review["body"].(string)

	// Build the feedback request.
	feedbackReq := map[string]interface{}{
		"pr_number":      int(prNumber),
		"repo_full_name": repoFullName,
		"review_body":    reviewBody,
		"comments":       []engine.ReviewComment{},
		"source":         "webhook:github:pull_request_review.submitted",
	}

	job := &Job{
		Type:    JobTypeFeedback,
		Request: feedbackReq,
	}
	jobID := s.jobs.Submit(job)

	s.logger.Info("webhook triggered feedback",
		"pr_number", int(prNumber),
		"repo", repoFullName,
		"job_id", jobID,
	)

	s.writeJSON(w, http.StatusOK, map[string]string{
		"status": "accepted",
		"job_id": jobID,
	})
}
