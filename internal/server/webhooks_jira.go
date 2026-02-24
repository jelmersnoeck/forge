package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jelmersnoeck/forge/pkg/config"
)

// handleJiraWebhook processes incoming Jira webhook events.
// It verifies the bearer token, parses the event, and dispatches
// matching actions to the job queue.
func (s *Server) handleJiraWebhook(w http.ResponseWriter, r *http.Request) {
	// Read the body.
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20)) // 10 MB limit
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_BODY", "failed to read request body")
		return
	}
	defer r.Body.Close()

	// Verify auth token.
	token := s.jiraAuthToken()
	if token == "" {
		s.logger.Error("jira webhook auth not configured")
		s.writeError(w, http.StatusInternalServerError, "CONFIG_ERROR", "jira webhook auth not configured")
		return
	}

	bearerToken := extractBearerToken(r)
	if bearerToken == "" {
		s.writeError(w, http.StatusUnauthorized, "MISSING_AUTH", "missing Authorization header")
		return
	}

	if bearerToken != token {
		s.writeError(w, http.StatusUnauthorized, "INVALID_AUTH", "invalid auth token")
		return
	}

	// Parse the payload.
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_JSON", "failed to parse webhook payload")
		return
	}

	// Extract event type from the webhookEvent field.
	webhookEvent, _ := payload["webhookEvent"].(string)
	if webhookEvent == "" {
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "missing webhookEvent"})
		return
	}

	s.logger.Info("received jira webhook", "event", webhookEvent)

	// Extract issue key.
	issueKey := jiraIssueKey(payload)
	if issueKey == "" {
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "no issue key in payload"})
		return
	}

	// Extract status transition (if any).
	newStatus := jiraStatusTransition(payload)

	// Extract issue labels.
	issueLabels := jiraIssueLabels(payload)

	// Match against configured triggers.
	triggers := s.jiraTriggers()
	for _, trigger := range triggers {
		if !jiraMatchTrigger(trigger, webhookEvent, newStatus, issueLabels) {
			continue
		}

		issueRef := fmt.Sprintf("jira://%s", issueKey)
		action := trigger.Action
		if action == "" {
			action = "build"
		}

		jobType := JobTypeBuild
		if action == "review" {
			jobType = JobTypeReview
		} else if action == "plan" {
			jobType = JobTypePlan
		}

		job := &Job{
			Type: jobType,
			Request: map[string]interface{}{
				"issue":  issueRef,
				"source": fmt.Sprintf("webhook:jira:%s", webhookEvent),
			},
		}
		jobID := s.jobs.Submit(job)

		s.logger.Info("jira webhook triggered job", "issue_ref", issueRef, "job_id", jobID, "action", action)
		s.writeJSON(w, http.StatusOK, map[string]string{
			"status": "accepted",
			"job_id": jobID,
		})
		return
	}

	// No trigger matched.
	s.writeJSON(w, http.StatusOK, map[string]string{
		"status": "ignored",
		"event":  webhookEvent,
	})
}

// jiraAuthToken returns the configured Jira webhook auth token.
func (s *Server) jiraAuthToken() string {
	if s.config.Webhooks.Jira != nil {
		return s.config.Webhooks.Jira.Auth
	}
	return ""
}

// jiraTriggers returns the configured Jira webhook triggers.
func (s *Server) jiraTriggers() []config.WebhookTrigger {
	if s.config.Webhooks.Jira != nil {
		return s.config.Webhooks.Jira.Triggers
	}
	return nil
}

// extractBearerToken extracts the bearer token from the Authorization header.
func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	const prefix = "Bearer "
	if len(auth) < len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return ""
	}
	return auth[len(prefix):]
}

// jiraIssueKey extracts the issue key from a Jira webhook payload.
func jiraIssueKey(payload map[string]interface{}) string {
	issue, _ := payload["issue"].(map[string]interface{})
	if issue == nil {
		return ""
	}
	key, _ := issue["key"].(string)
	return key
}

// jiraStatusTransition extracts the new status value from a Jira changelog.
// It looks at changelog.items for a field named "status" and returns the
// toString value.
func jiraStatusTransition(payload map[string]interface{}) string {
	changelog, _ := payload["changelog"].(map[string]interface{})
	if changelog == nil {
		return ""
	}
	items, _ := changelog["items"].([]interface{})
	for _, item := range items {
		entry, _ := item.(map[string]interface{})
		if entry == nil {
			continue
		}
		field, _ := entry["field"].(string)
		if strings.EqualFold(field, "status") {
			toString, _ := entry["toString"].(string)
			return toString
		}
	}
	return ""
}

// jiraIssueLabels extracts the labels from a Jira issue payload.
func jiraIssueLabels(payload map[string]interface{}) []string {
	issue, _ := payload["issue"].(map[string]interface{})
	if issue == nil {
		return nil
	}
	fields, _ := issue["fields"].(map[string]interface{})
	if fields == nil {
		return nil
	}
	rawLabels, _ := fields["labels"].([]interface{})
	labels := make([]string, 0, len(rawLabels))
	for _, l := range rawLabels {
		if s, ok := l.(string); ok {
			labels = append(labels, s)
		}
	}
	return labels
}

// jiraMatchTrigger checks if a Jira event matches a configured trigger.
func jiraMatchTrigger(trigger config.WebhookTrigger, webhookEvent, newStatus string, issueLabels []string) bool {
	// Event must match if specified.
	if trigger.Event != "" && trigger.Event != webhookEvent {
		return false
	}

	// TransitionTo must match (case-insensitive) if specified.
	if trigger.TransitionTo != "" {
		if !strings.EqualFold(trigger.TransitionTo, newStatus) {
			return false
		}
	}

	// Label must be present if specified.
	if trigger.Label != "" {
		found := false
		for _, l := range issueLabels {
			if strings.EqualFold(l, trigger.Label) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}
