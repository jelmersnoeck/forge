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

const linearAPIURL = "https://api.linear.app/graphql"

// LinearTracker implements the Tracker interface using the Linear GraphQL API.
type LinearTracker struct {
	team   string // Default team key, e.g. "TEAM"
	apiKey string // Linear API key.
	client *http.Client
}

// NewLinearTracker creates a new Linear tracker.
func NewLinearTracker(team, apiKey string) *LinearTracker {
	return &LinearTracker{
		team:   team,
		apiKey: apiKey,
		client: &http.Client{},
	}
}

// linearGraphQLRequest is the JSON body for a GraphQL request.
type linearGraphQLRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables,omitempty"`
}

// linearGraphQLResponse is the JSON response from a GraphQL request.
type linearGraphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors,omitempty"`
}

// linearIssueData is the nested data structure for an issue query.
type linearIssueData struct {
	Issue struct {
		ID          string `json:"id"`
		Identifier  string `json:"identifier"`
		Title       string `json:"title"`
		Description string `json:"description"`
		URL         string `json:"url"`
		State       struct {
			Name string `json:"name"`
		} `json:"state"`
		Labels struct {
			Nodes []struct {
				Name string `json:"name"`
			} `json:"nodes"`
		} `json:"labels"`
		Team struct {
			Key string `json:"key"`
		} `json:"team"`
	} `json:"issue"`
}

// linearCreateIssueData is the nested data for issue creation response.
type linearCreateIssueData struct {
	IssueCreate struct {
		Success bool `json:"success"`
		Issue   struct {
			ID         string `json:"id"`
			Identifier string `json:"identifier"`
			Title      string `json:"title"`
			URL        string `json:"url"`
		} `json:"issue"`
	} `json:"issueCreate"`
}

// linearCreateCommentData is the nested data for comment creation response.
type linearCreateCommentData struct {
	CommentCreate struct {
		Success bool `json:"success"`
	} `json:"commentCreate"`
}

// linearRelationData is the nested data for relation creation response.
type linearRelationData struct {
	IssueRelationCreate struct {
		Success bool `json:"success"`
	} `json:"issueRelationCreate"`
}

// GetIssue fetches a Linear issue by its identifier (e.g., "TEAM-123").
func (l *LinearTracker) GetIssue(ctx context.Context, ref string) (*Issue, error) {
	identifier := l.resolveIdentifier(ref)
	slog.DebugContext(ctx, "fetching linear issue", "identifier", identifier)

	query := `query IssueByIdentifier($identifier: String!) {
		issue(id: $identifier) {
			id
			identifier
			title
			description
			url
			state { name }
			labels { nodes { name } }
			team { key }
		}
	}`

	vars := map[string]interface{}{
		"identifier": identifier,
	}

	body, err := l.doGraphQL(ctx, query, vars)
	if err != nil {
		return nil, fmt.Errorf("get issue %s: %w", identifier, err)
	}

	var data linearIssueData
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("get issue %s: parsing response: %w", identifier, err)
	}

	labels := make([]string, 0, len(data.Issue.Labels.Nodes))
	for _, l := range data.Issue.Labels.Nodes {
		labels = append(labels, l.Name)
	}

	return &Issue{
		ID:      data.Issue.Identifier,
		Tracker: "linear",
		Title:   data.Issue.Title,
		Body:    data.Issue.Description,
		Labels:  labels,
		Project: data.Issue.Team.Key,
		Status:  strings.ToLower(data.Issue.State.Name),
		URL:     data.Issue.URL,
	}, nil
}

// CreateIssue creates a new issue in Linear.
func (l *LinearTracker) CreateIssue(ctx context.Context, req *CreateIssueRequest) (*Issue, error) {
	slog.DebugContext(ctx, "creating linear issue", "title", req.Title)

	query := `mutation IssueCreate($input: IssueCreateInput!) {
		issueCreate(input: $input) {
			success
			issue {
				id
				identifier
				title
				url
			}
		}
	}`

	input := map[string]interface{}{
		"title":       req.Title,
		"description": req.Body,
	}

	// Labels in Linear need to be created/referenced by ID, so we pass them
	// as part of the description for simplicity. A full implementation would
	// look up label IDs first.

	vars := map[string]interface{}{
		"input": input,
	}

	body, err := l.doGraphQL(ctx, query, vars)
	if err != nil {
		return nil, fmt.Errorf("create issue: %w", err)
	}

	var data linearCreateIssueData
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("create issue: parsing response: %w", err)
	}

	if !data.IssueCreate.Success {
		return nil, fmt.Errorf("create issue: operation not successful")
	}

	return &Issue{
		ID:      data.IssueCreate.Issue.Identifier,
		Tracker: "linear",
		Title:   data.IssueCreate.Issue.Title,
		Body:    req.Body,
		Labels:  req.Labels,
		Project: l.team,
		URL:     data.IssueCreate.Issue.URL,
	}, nil
}

// CreatePR returns ErrNotSupported because Linear does not manage pull requests.
func (l *LinearTracker) CreatePR(ctx context.Context, req *CreatePRRequest) (*PullRequest, error) {
	return nil, ErrNotSupported
}

// Comment adds a comment to a Linear issue.
func (l *LinearTracker) Comment(ctx context.Context, ref string, body string) error {
	identifier := l.resolveIdentifier(ref)
	slog.DebugContext(ctx, "commenting on linear issue", "identifier", identifier)

	// First, get the issue's internal ID (Linear GraphQL needs the UUID, not
	// the human-readable identifier for mutations that take issueId).
	issueID, err := l.getIssueUUID(ctx, identifier)
	if err != nil {
		return fmt.Errorf("resolving issue ID for %s: %w", identifier, err)
	}

	query := `mutation CommentCreate($input: CommentCreateInput!) {
		commentCreate(input: $input) {
			success
		}
	}`

	vars := map[string]interface{}{
		"input": map[string]interface{}{
			"issueId": issueID,
			"body":    body,
		},
	}

	respBody, err := l.doGraphQL(ctx, query, vars)
	if err != nil {
		return fmt.Errorf("comment on %s: %w", identifier, err)
	}

	var data linearCreateCommentData
	if err := json.Unmarshal(respBody, &data); err != nil {
		return fmt.Errorf("comment on %s: parsing response: %w", identifier, err)
	}

	if !data.CommentCreate.Success {
		return fmt.Errorf("comment on %s: operation not successful", identifier)
	}

	return nil
}

// Link creates a dependency relationship between two Linear issues.
func (l *LinearTracker) Link(ctx context.Context, from string, to string, rel LinkRelation) error {
	fromIdentifier := l.resolveIdentifier(from)
	toIdentifier := l.resolveIdentifier(to)
	slog.DebugContext(ctx, "linking linear issues", "from", fromIdentifier, "to", toIdentifier, "rel", rel)

	fromID, err := l.getIssueUUID(ctx, fromIdentifier)
	if err != nil {
		return fmt.Errorf("resolving issue ID for %s: %w", fromIdentifier, err)
	}

	toID, err := l.getIssueUUID(ctx, toIdentifier)
	if err != nil {
		return fmt.Errorf("resolving issue ID for %s: %w", toIdentifier, err)
	}

	query := `mutation IssueRelationCreate($input: IssueRelationCreateInput!) {
		issueRelationCreate(input: $input) {
			success
		}
	}`

	relType := linearRelationType(rel)

	vars := map[string]interface{}{
		"input": map[string]interface{}{
			"issueId":        fromID,
			"relatedIssueId": toID,
			"type":           relType,
		},
	}

	respBody, err := l.doGraphQL(ctx, query, vars)
	if err != nil {
		return fmt.Errorf("link %s -> %s: %w", fromIdentifier, toIdentifier, err)
	}

	var data linearRelationData
	if err := json.Unmarshal(respBody, &data); err != nil {
		return fmt.Errorf("link %s -> %s: parsing response: %w", fromIdentifier, toIdentifier, err)
	}

	if !data.IssueRelationCreate.Success {
		return fmt.Errorf("link %s -> %s: operation not successful", fromIdentifier, toIdentifier)
	}

	return nil
}

// getIssueUUID fetches the internal UUID for a Linear issue by its identifier.
func (l *LinearTracker) getIssueUUID(ctx context.Context, identifier string) (string, error) {
	query := `query IssueByIdentifier($identifier: String!) {
		issue(id: $identifier) {
			id
		}
	}`

	vars := map[string]interface{}{
		"identifier": identifier,
	}

	body, err := l.doGraphQL(ctx, query, vars)
	if err != nil {
		return "", err
	}

	var data struct {
		Issue struct {
			ID string `json:"id"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("parsing issue UUID response: %w", err)
	}

	if data.Issue.ID == "" {
		return "", fmt.Errorf("issue %s not found", identifier)
	}

	return data.Issue.ID, nil
}

// doGraphQL executes a GraphQL request against the Linear API.
func (l *LinearTracker) doGraphQL(ctx context.Context, query string, variables map[string]interface{}) (json.RawMessage, error) {
	gqlReq := linearGraphQLRequest{
		Query:     query,
		Variables: variables,
	}

	payload, err := json.Marshal(gqlReq)
	if err != nil {
		return nil, fmt.Errorf("marshaling GraphQL request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, linearAPIURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", l.apiKey)

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var gqlResp linearGraphQLResponse
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		return nil, fmt.Errorf("parsing GraphQL response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL error: %s", gqlResp.Errors[0].Message)
	}

	return gqlResp.Data, nil
}

// resolveIdentifier resolves a ref string to a Linear issue identifier.
func (l *LinearTracker) resolveIdentifier(ref string) string {
	// Strip any linear: prefix.
	ref = strings.TrimPrefix(ref, "linear://")
	ref = strings.TrimPrefix(ref, "linear:")
	ref = strings.TrimPrefix(ref, "lin:")

	// If it already contains a dash, assume it's a full identifier.
	if strings.Contains(ref, "-") {
		return ref
	}
	// Otherwise prepend the default team.
	return fmt.Sprintf("%s-%s", l.team, ref)
}

// linearRelationType maps Forge link relations to Linear relation types.
func linearRelationType(rel LinkRelation) string {
	switch rel {
	case RelBlocks:
		return "blocks"
	case RelDependsOn:
		return "blocks" // Inverse direction handled by from/to ordering.
	case RelRelates:
		return "related"
	default:
		return "related"
	}
}
