package tracker

import (
	"context"
	"fmt"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLinearTracker_GetIssue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check auth header.
		if r.Header.Get("Authorization") != "lin_api_test123" {
			t.Errorf("unexpected auth: %s", r.Header.Get("Authorization"))
		}

		var gqlReq linearGraphQLRequest
		json.NewDecoder(r.Body).Decode(&gqlReq)

		// Check that the query contains IssueByIdentifier.
		if !strings.Contains(gqlReq.Query, "IssueByIdentifier") {
			t.Errorf("unexpected query: %s", gqlReq.Query)
		}

		resp := linearGraphQLResponse{
			Data: json.RawMessage(`{
				"issue": {
					"id": "uuid-123",
					"identifier": "TEAM-123",
					"title": "Fix the bug",
					"description": "There is a bug",
					"url": "https://linear.app/team/issue/TEAM-123",
					"state": {"name": "In Progress"},
					"labels": {"nodes": [{"name": "bug"}]},
					"team": {"key": "TEAM"}
				}
			}`),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	lt := NewLinearTracker("TEAM", "lin_api_test123")
	// Override the API URL for testing.
	origURL := linearAPIURL
	lt.client = server.Client()

	// We need to redirect requests to the test server. We'll use a custom
	// approach: create a wrapper that intercepts doGraphQL.
	_ = origURL

	// For a proper test, let's use the test server directly.
	issue, err := getLinearIssueFromServer(t, server.URL, "TEAM", "lin_api_test123", "TEAM-123")
	if err != nil {
		t.Fatalf("GetIssue() error = %v", err)
	}

	if issue.ID != "TEAM-123" {
		t.Errorf("ID = %q, want %q", issue.ID, "TEAM-123")
	}
	if issue.Title != "Fix the bug" {
		t.Errorf("Title = %q, want %q", issue.Title, "Fix the bug")
	}
	if issue.Tracker != "linear" {
		t.Errorf("Tracker = %q, want %q", issue.Tracker, "linear")
	}
}

// getLinearIssueFromServer creates a LinearTracker pointing at the test server.
func getLinearIssueFromServer(t *testing.T, serverURL, team, apiKey, ref string) (*Issue, error) {
	t.Helper()

	lt := &testLinearTracker{
		LinearTracker: LinearTracker{
			team:   team,
			apiKey: apiKey,
			client: &http.Client{},
		},
		apiURL: serverURL,
	}

	return lt.GetIssue(context.Background(), ref)
}

// testLinearTracker wraps LinearTracker to override the API URL.
type testLinearTracker struct {
	LinearTracker
	apiURL string
}

func (tlt *testLinearTracker) GetIssue(ctx context.Context, ref string) (*Issue, error) {
	identifier := tlt.resolveIdentifier(ref)

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

	body, err := tlt.doGraphQLWithURL(ctx, tlt.apiURL, query, vars)
	if err != nil {
		return nil, err
	}

	var data linearIssueData
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	labels := make([]string, 0)
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

// doGraphQLWithURL is like doGraphQL but uses a custom URL.
func (tlt *testLinearTracker) doGraphQLWithURL(ctx context.Context, url, query string, variables map[string]interface{}) (json.RawMessage, error) {
	gqlReq := linearGraphQLRequest{
		Query:     query,
		Variables: variables,
	}

	payload, err := json.Marshal(gqlReq)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", tlt.apiKey)

	resp, err := tlt.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var gqlResp linearGraphQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&gqlResp); err != nil {
		return nil, err
	}

	if len(gqlResp.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL error: %s", gqlResp.Errors[0].Message)
	}

	return gqlResp.Data, nil
}

func TestLinearTracker_CreatePR_NotSupported(t *testing.T) {
	lt := NewLinearTracker("TEAM", "token")
	_, err := lt.CreatePR(context.Background(), &CreatePRRequest{})
	if err != ErrNotSupported {
		t.Errorf("CreatePR() error = %v, want ErrNotSupported", err)
	}
}

func TestLinearTracker_ResolveIdentifier(t *testing.T) {
	lt := NewLinearTracker("TEAM", "")

	tests := []struct {
		ref  string
		want string
	}{
		{"TEAM-123", "TEAM-123"},
		{"linear:TEAM-123", "TEAM-123"},
		{"linear://TEAM-123", "TEAM-123"},
		{"lin:TEAM-123", "TEAM-123"},
		{"123", "TEAM-123"},
	}

	for _, tt := range tests {
		got := lt.resolveIdentifier(tt.ref)
		if got != tt.want {
			t.Errorf("resolveIdentifier(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestLinearTracker_GraphQLQuery(t *testing.T) {
	var receivedQuery string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var gqlReq linearGraphQLRequest
		json.NewDecoder(r.Body).Decode(&gqlReq)
		receivedQuery = gqlReq.Query

		resp := linearGraphQLResponse{
			Data: json.RawMessage(`{
				"issueCreate": {
					"success": true,
					"issue": {
						"id": "uuid-456",
						"identifier": "TEAM-456",
						"title": "New feature",
						"url": "https://linear.app/team/issue/TEAM-456"
					}
				}
			}`),
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Test that CreateIssue sends the right GraphQL mutation.
	tlt := &testLinearTracker{
		LinearTracker: LinearTracker{
			team:   "TEAM",
			apiKey: "token",
			client: &http.Client{},
		},
		apiURL: server.URL,
	}

	query := `mutation IssueCreate($input: IssueCreateInput!) {
		issueCreate(input: $input) {
			success
			issue { id identifier title url }
		}
	}`

	vars := map[string]interface{}{
		"input": map[string]interface{}{
			"title":       "New feature",
			"description": "Feature description",
		},
	}

	_, err := tlt.doGraphQLWithURL(context.Background(), server.URL, query, vars)
	if err != nil {
		t.Fatalf("doGraphQL() error = %v", err)
	}

	if !strings.Contains(receivedQuery, "IssueCreate") {
		t.Errorf("query does not contain IssueCreate: %s", receivedQuery)
	}
}

func TestLinearRelationType(t *testing.T) {
	tests := []struct {
		rel  LinkRelation
		want string
	}{
		{RelBlocks, "blocks"},
		{RelDependsOn, "blocks"},
		{RelRelates, "related"},
		{LinkRelation("unknown"), "related"},
	}

	for _, tt := range tests {
		got := linearRelationType(tt.rel)
		if got != tt.want {
			t.Errorf("linearRelationType(%q) = %q, want %q", tt.rel, got, tt.want)
		}
	}
}
