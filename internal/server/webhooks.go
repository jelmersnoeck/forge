package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// handleGitHubWebhook processes incoming GitHub webhook events.
// It verifies the HMAC-SHA256 signature, parses the event, and dispatches
// matching actions to the job queue.
func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	// Read the body for signature verification.
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20)) // 10 MB limit
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_BODY", "failed to read request body")
		return
	}
	defer r.Body.Close()

	// CRITICAL: Verify webhook signature.
	secret := s.webhookSecret()
	if secret == "" {
		s.logger.Error("github webhook secret not configured")
		s.writeError(w, http.StatusInternalServerError, "CONFIG_ERROR", "webhook secret not configured")
		return
	}

	signature := r.Header.Get("X-Hub-Signature-256")
	if signature == "" {
		s.writeError(w, http.StatusUnauthorized, "MISSING_SIGNATURE", "missing X-Hub-Signature-256 header")
		return
	}

	if !verifyGitHubSignature(body, signature, secret) {
		s.writeError(w, http.StatusUnauthorized, "INVALID_SIGNATURE", "webhook signature verification failed")
		return
	}

	// Parse event type from header.
	eventType := r.Header.Get("X-GitHub-Event")
	if eventType == "" {
		s.writeError(w, http.StatusBadRequest, "MISSING_EVENT", "missing X-GitHub-Event header")
		return
	}

	s.logger.Info("received github webhook", "event", eventType)

	// Parse the payload.
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_JSON", "failed to parse webhook payload")
		return
	}

	// Dispatch based on event type and action.
	action, _ := payload["action"].(string)

	switch {
	case eventType == "issues" && action == "opened":
		s.handleIssueOpened(w, payload)
	case eventType == "pull_request" && action == "opened":
		s.handlePROpened(w, payload)
	case eventType == "issue_comment" && action == "created":
		s.handleIssueComment(w, payload)
	default:
		// Acknowledge but take no action.
		s.writeJSON(w, http.StatusOK, map[string]string{
			"status": "ignored",
			"event":  eventType,
			"action": action,
		})
	}
}

// handleIssueOpened triggers a build if the issue has the "forge" label.
func (s *Server) handleIssueOpened(w http.ResponseWriter, payload map[string]interface{}) {
	issue, _ := payload["issue"].(map[string]interface{})
	if issue == nil {
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "no issue in payload"})
		return
	}

	// Check for the "forge" label.
	if !hasLabel(issue, "forge") {
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "missing forge label"})
		return
	}

	issueRef := buildIssueRef(payload)
	if issueRef == "" {
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "could not build issue ref"})
		return
	}

	job := &Job{
		Type: JobTypeBuild,
		Request: map[string]interface{}{
			"issue":  issueRef,
			"source": "webhook:github:issues.opened",
		},
	}
	jobID := s.jobs.Submit(job)

	s.logger.Info("webhook triggered build", "issue_ref", issueRef, "job_id", jobID)
	s.writeJSON(w, http.StatusOK, map[string]string{
		"status": "accepted",
		"job_id": jobID,
	})
}

// handlePROpened triggers a review for a newly opened pull request.
func (s *Server) handlePROpened(w http.ResponseWriter, payload map[string]interface{}) {
	pr, _ := payload["pull_request"].(map[string]interface{})
	if pr == nil {
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "no pull_request in payload"})
		return
	}

	prNumber := fmt.Sprintf("%.0f", pr["number"])
	repo, _ := payload["repository"].(map[string]interface{})
	repoFullName, _ := repo["full_name"].(string)

	job := &Job{
		Type: JobTypeReview,
		Request: map[string]interface{}{
			"pr_number": prNumber,
			"repo":      repoFullName,
			"source":    "webhook:github:pull_request.opened",
		},
	}
	jobID := s.jobs.Submit(job)

	s.logger.Info("webhook triggered review", "pr", prNumber, "repo", repoFullName, "job_id", jobID)
	s.writeJSON(w, http.StatusOK, map[string]string{
		"status": "accepted",
		"job_id": jobID,
	})
}

// handleIssueComment checks for /forge commands in issue comments.
func (s *Server) handleIssueComment(w http.ResponseWriter, payload map[string]interface{}) {
	comment, _ := payload["comment"].(map[string]interface{})
	if comment == nil {
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "no comment in payload"})
		return
	}

	body, _ := comment["body"].(string)
	body = strings.TrimSpace(body)

	if !strings.HasPrefix(body, "/forge build") {
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "not a forge command"})
		return
	}

	issueRef := buildIssueRef(payload)
	if issueRef == "" {
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "could not build issue ref"})
		return
	}

	job := &Job{
		Type: JobTypeBuild,
		Request: map[string]interface{}{
			"issue":  issueRef,
			"source": "webhook:github:issue_comment.created",
		},
	}
	jobID := s.jobs.Submit(job)

	s.logger.Info("webhook command triggered build", "issue_ref", issueRef, "job_id", jobID)
	s.writeJSON(w, http.StatusOK, map[string]string{
		"status": "accepted",
		"job_id": jobID,
	})
}

// verifyGitHubSignature checks the HMAC-SHA256 signature of a GitHub webhook.
func verifyGitHubSignature(payload []byte, signatureHeader, secret string) bool {
	// The header value is "sha256=<hex>".
	if !strings.HasPrefix(signatureHeader, "sha256=") {
		return false
	}
	sigHex := strings.TrimPrefix(signatureHeader, "sha256=")
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := mac.Sum(nil)
	return hmac.Equal(sig, expected)
}

// webhookSecret returns the configured GitHub webhook secret.
func (s *Server) webhookSecret() string {
	if s.config.Webhooks.GitHub != nil {
		return s.config.Webhooks.GitHub.Secret
	}
	return ""
}

// hasLabel checks if a GitHub issue payload contains a specific label.
func hasLabel(issue map[string]interface{}, labelName string) bool {
	labels, ok := issue["labels"].([]interface{})
	if !ok {
		return false
	}
	for _, l := range labels {
		label, ok := l.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := label["name"].(string)
		if name == labelName {
			return true
		}
	}
	return false
}

// buildIssueRef constructs a Forge issue reference from a GitHub webhook payload.
func buildIssueRef(payload map[string]interface{}) string {
	repo, _ := payload["repository"].(map[string]interface{})
	if repo == nil {
		return ""
	}
	fullName, _ := repo["full_name"].(string)
	if fullName == "" {
		return ""
	}

	issue, _ := payload["issue"].(map[string]interface{})
	if issue == nil {
		return ""
	}
	number, ok := issue["number"].(float64)
	if !ok {
		return ""
	}

	return fmt.Sprintf("gh:%s#%d", fullName, int(number))
}
