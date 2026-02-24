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

	"github.com/jelmersnoeck/forge/pkg/config"
)

// handleLinearWebhook processes incoming Linear webhook events.
// It verifies the HMAC-SHA256 signature, parses the event, and dispatches
// matching actions to the job queue.
func (s *Server) handleLinearWebhook(w http.ResponseWriter, r *http.Request) {
	// Read the body for signature verification.
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20)) // 10 MB limit
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_BODY", "failed to read request body")
		return
	}
	defer r.Body.Close()

	// Verify webhook signature.
	secret := s.linearSigningSecret()
	if secret == "" {
		s.logger.Error("linear webhook signing secret not configured")
		s.writeError(w, http.StatusInternalServerError, "CONFIG_ERROR", "linear webhook signing secret not configured")
		return
	}

	signature := r.Header.Get("Linear-Signature")
	if signature == "" {
		s.writeError(w, http.StatusUnauthorized, "MISSING_SIGNATURE", "missing Linear-Signature header")
		return
	}

	if !verifyLinearSignature(body, signature, secret) {
		s.writeError(w, http.StatusUnauthorized, "INVALID_SIGNATURE", "webhook signature verification failed")
		return
	}

	// Parse the payload.
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_JSON", "failed to parse webhook payload")
		return
	}

	// Extract event info.
	action, _ := payload["action"].(string)
	eventType, _ := payload["type"].(string)

	if eventType == "" || action == "" {
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "missing type or action"})
		return
	}

	// Compose the event string as "Type.action" (e.g., "Issue.update").
	eventStr := eventType + "." + action

	s.logger.Info("received linear webhook", "event", eventStr)

	// Extract issue identifier from data.
	identifier := linearIssueIdentifier(payload)
	if identifier == "" {
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "no issue identifier in payload"})
		return
	}

	// Extract labels from data.
	labels := linearLabels(payload)

	// Match against configured triggers.
	triggers := s.linearTriggers()
	for _, trigger := range triggers {
		if !linearMatchTrigger(trigger, eventStr, labels) {
			continue
		}

		issueRef := fmt.Sprintf("linear://%s", identifier)
		triggerAction := trigger.Action
		if triggerAction == "" {
			triggerAction = "build"
		}

		jobType := JobTypeBuild
		if triggerAction == "review" {
			jobType = JobTypeReview
		} else if triggerAction == "plan" {
			jobType = JobTypePlan
		}

		job := &Job{
			Type: jobType,
			Request: map[string]interface{}{
				"issue":  issueRef,
				"source": fmt.Sprintf("webhook:linear:%s", eventStr),
			},
		}
		jobID := s.jobs.Submit(job)

		s.logger.Info("linear webhook triggered job", "issue_ref", issueRef, "job_id", jobID, "action", triggerAction)
		s.writeJSON(w, http.StatusOK, map[string]string{
			"status": "accepted",
			"job_id": jobID,
		})
		return
	}

	// No trigger matched.
	s.writeJSON(w, http.StatusOK, map[string]string{
		"status": "ignored",
		"event":  eventStr,
	})
}

// linearSigningSecret returns the configured Linear webhook signing secret.
func (s *Server) linearSigningSecret() string {
	if s.config.Webhooks.Linear != nil {
		return s.config.Webhooks.Linear.SigningSecret
	}
	return ""
}

// linearTriggers returns the configured Linear webhook triggers.
func (s *Server) linearTriggers() []config.WebhookTrigger {
	if s.config.Webhooks.Linear != nil {
		return s.config.Webhooks.Linear.Triggers
	}
	return nil
}

// verifyLinearSignature checks the HMAC-SHA256 signature of a Linear webhook.
// Linear sends the raw hex-encoded HMAC in the Linear-Signature header.
func verifyLinearSignature(payload []byte, signatureHeader, secret string) bool {
	sig, err := hex.DecodeString(signatureHeader)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := mac.Sum(nil)
	return hmac.Equal(sig, expected)
}

// linearIssueIdentifier extracts the issue identifier from a Linear webhook payload.
// It looks at data.identifier (e.g., "TEAM-123").
func linearIssueIdentifier(payload map[string]interface{}) string {
	data, _ := payload["data"].(map[string]interface{})
	if data == nil {
		return ""
	}
	identifier, _ := data["identifier"].(string)
	return identifier
}

// linearLabels extracts label names from a Linear webhook payload.
// Linear sends labels as an array of objects with a "name" field under data.labels.
func linearLabels(payload map[string]interface{}) []string {
	data, _ := payload["data"].(map[string]interface{})
	if data == nil {
		return nil
	}

	rawLabels, _ := data["labels"].([]interface{})
	labels := make([]string, 0, len(rawLabels))
	for _, l := range rawLabels {
		label, ok := l.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := label["name"].(string)
		if name != "" {
			labels = append(labels, name)
		}
	}
	return labels
}

// linearMatchTrigger checks if a Linear event matches a configured trigger.
func linearMatchTrigger(trigger config.WebhookTrigger, eventStr string, labels []string) bool {
	// Event must match if specified.
	if trigger.Event != "" && trigger.Event != eventStr {
		return false
	}

	// Label must be present if specified.
	if trigger.Label != "" {
		found := false
		for _, l := range labels {
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
