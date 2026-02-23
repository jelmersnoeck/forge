package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)


// JiraTracker implements the Tracker interface using the Jira REST API.
type JiraTracker struct {
	instance string // Base URL, e.g. "https://org.atlassian.net"
	project  string // Default project key, e.g. "PROJ"
	email    string // User email for Basic auth.
	token    string // API token for Basic auth.
	client   *http.Client
}

// NewJiraTracker creates a new Jira tracker.
func NewJiraTracker(instance, project, email, token string) *JiraTracker {
	return &JiraTracker{
		instance: strings.TrimRight(instance, "/"),
		project:  project,
		email:    email,
		token:    token,
		client:   &http.Client{},
	}
}

// jiraIssueResponse is the JSON structure returned by Jira's issue API.
type jiraIssueResponse struct {
	Key    string `json:"key"`
	ID     string `json:"id"`
	Self   string `json:"self"`
	Fields struct {
		Summary     string `json:"summary"`
		Description string `json:"description"`
		Status      struct {
			Name string `json:"name"`
		} `json:"status"`
		Labels    []string `json:"labels"`
		IssueType struct {
			Name string `json:"name"`
		} `json:"issuetype"`
		Project struct {
			Key string `json:"key"`
		} `json:"project"`
	} `json:"fields"`
}

// jiraCreateRequest is the JSON body for creating a Jira issue.
type jiraCreateRequest struct {
	Fields jiraCreateFields `json:"fields"`
}

// jiraCreateFields contains the fields for issue creation.
type jiraCreateFields struct {
	Project   jiraProject   `json:"project"`
	Summary   string        `json:"summary"`
	Description string      `json:"description"`
	IssueType jiraIssueType `json:"issuetype"`
	Labels    []string      `json:"labels,omitempty"`
}

type jiraProject struct {
	Key string `json:"key"`
}

type jiraIssueType struct {
	Name string `json:"name"`
}

// jiraCreateResponse is the JSON response from creating an issue.
type jiraCreateResponse struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Self string `json:"self"`
}

// jiraLinkRequest is the JSON body for creating an issue link.
type jiraLinkRequest struct {
	Type         jiraLinkType      `json:"type"`
	InwardIssue  jiraLinkIssueRef  `json:"inwardIssue"`
	OutwardIssue jiraLinkIssueRef  `json:"outwardIssue"`
}

type jiraLinkType struct {
	Name string `json:"name"`
}

type jiraLinkIssueRef struct {
	Key string `json:"key"`
}

// GetIssue fetches a Jira issue by its key (e.g., "PROJ-123").
func (j *JiraTracker) GetIssue(ctx context.Context, ref string) (*Issue, error) {
	key := j.resolveKey(ref)
	slog.DebugContext(ctx, "fetching jira issue", "key", key)

	url := fmt.Sprintf("%s/rest/api/3/issue/%s", j.instance, key)
	body, err := j.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("get issue %s: %w", key, err)
	}

	var resp jiraIssueResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("get issue %s: parsing response: %w", key, err)
	}

	return &Issue{
		ID:      resp.Key,
		Tracker: "jira",
		Title:   resp.Fields.Summary,
		Body:    resp.Fields.Description,
		Labels:  resp.Fields.Labels,
		Project: resp.Fields.Project.Key,
		Status:  strings.ToLower(resp.Fields.Status.Name),
		URL:     fmt.Sprintf("%s/browse/%s", j.instance, resp.Key),
	}, nil
}

// CreateIssue creates a new issue in Jira.
func (j *JiraTracker) CreateIssue(ctx context.Context, req *CreateIssueRequest) (*Issue, error) {
	slog.DebugContext(ctx, "creating jira issue", "title", req.Title)

	project := req.Project
	if project == "" {
		project = j.project
	}

	createReq := jiraCreateRequest{
		Fields: jiraCreateFields{
			Project:     jiraProject{Key: project},
			Summary:     req.Title,
			Description: req.Body,
			IssueType:   jiraIssueType{Name: "Task"},
			Labels:      req.Labels,
		},
	}

	payload, err := json.Marshal(createReq)
	if err != nil {
		return nil, fmt.Errorf("marshaling create request: %w", err)
	}

	url := fmt.Sprintf("%s/rest/api/3/issue", j.instance)
	body, err := j.doRequest(ctx, http.MethodPost, url, payload)
	if err != nil {
		return nil, fmt.Errorf("create issue: %w", err)
	}

	var resp jiraCreateResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("create issue: parsing response: %w", err)
	}

	return &Issue{
		ID:      resp.Key,
		Tracker: "jira",
		Title:   req.Title,
		Body:    req.Body,
		Labels:  req.Labels,
		Project: project,
		URL:     fmt.Sprintf("%s/browse/%s", j.instance, resp.Key),
	}, nil
}

// CreatePR returns ErrNotSupported because Jira does not manage pull requests.
func (j *JiraTracker) CreatePR(ctx context.Context, req *CreatePRRequest) (*PullRequest, error) {
	return nil, ErrNotSupported
}

// Comment adds a comment to a Jira issue.
func (j *JiraTracker) Comment(ctx context.Context, ref string, body string) error {
	key := j.resolveKey(ref)
	slog.DebugContext(ctx, "commenting on jira issue", "key", key)

	// Jira REST API v3 uses Atlassian Document Format for comments.
	commentBody := map[string]interface{}{
		"body": map[string]interface{}{
			"type":    "doc",
			"version": 1,
			"content": []map[string]interface{}{
				{
					"type": "paragraph",
					"content": []map[string]interface{}{
						{
							"type": "text",
							"text": body,
						},
					},
				},
			},
		},
	}

	payload, err := json.Marshal(commentBody)
	if err != nil {
		return fmt.Errorf("marshaling comment: %w", err)
	}

	url := fmt.Sprintf("%s/rest/api/3/issue/%s/comment", j.instance, key)
	_, err = j.doRequest(ctx, http.MethodPost, url, payload)
	if err != nil {
		return fmt.Errorf("comment on %s: %w", key, err)
	}
	return nil
}

// Link creates a dependency link between two Jira issues using issue links.
func (j *JiraTracker) Link(ctx context.Context, from string, to string, rel LinkRelation) error {
	fromKey := j.resolveKey(from)
	toKey := j.resolveKey(to)
	slog.DebugContext(ctx, "linking jira issues", "from", fromKey, "to", toKey, "rel", rel)

	linkType := jiraLinkTypeName(rel)

	linkReq := jiraLinkRequest{
		Type:         jiraLinkType{Name: linkType},
		InwardIssue:  jiraLinkIssueRef{Key: fromKey},
		OutwardIssue: jiraLinkIssueRef{Key: toKey},
	}

	payload, err := json.Marshal(linkReq)
	if err != nil {
		return fmt.Errorf("marshaling link request: %w", err)
	}

	url := fmt.Sprintf("%s/rest/api/3/issueLink", j.instance)
	_, err = j.doRequest(ctx, http.MethodPost, url, payload)
	if err != nil {
		return fmt.Errorf("link %s -> %s: %w", fromKey, toKey, err)
	}
	return nil
}

// doRequest executes an authenticated HTTP request against the Jira API.
func (j *JiraTracker) doRequest(ctx context.Context, method, url string, body []byte) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.SetBasicAuth(j.email, j.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := j.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// resolveKey resolves a ref string to a Jira issue key.
// If it looks like a bare number, it prepends the default project.
func (j *JiraTracker) resolveKey(ref string) string {
	// If it already contains a dash, assume it's a full key.
	if strings.Contains(ref, "-") {
		// Strip any jira: prefix.
		ref = strings.TrimPrefix(ref, "jira://")
		ref = strings.TrimPrefix(ref, "jira:")
		return ref
	}
	// Otherwise prepend the default project.
	return fmt.Sprintf("%s-%s", j.project, ref)
}

// jiraLinkTypeName maps Forge link relations to Jira issue link type names.
func jiraLinkTypeName(rel LinkRelation) string {
	switch rel {
	case RelBlocks:
		return "Blocks"
	case RelDependsOn:
		return "Blocks" // In Jira, "Blocks" is the inverse relationship.
	case RelRelates:
		return "Relates"
	default:
		return "Relates"
	}
}
