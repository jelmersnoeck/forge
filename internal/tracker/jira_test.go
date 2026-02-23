package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJiraTracker_GetIssue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/issue/PROJ-123" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}

		// Check auth header.
		user, _, ok := r.BasicAuth()
		if !ok || user != "user@example.com" {
			t.Errorf("unexpected auth: %s", r.Header.Get("Authorization"))
		}

		resp := jiraIssueResponse{
			Key: "PROJ-123",
			ID:  "10001",
		}
		resp.Fields.Summary = "Fix the bug"
		resp.Fields.Description = "There is a bug"
		resp.Fields.Status.Name = "In Progress"
		resp.Fields.Labels = []string{"bug", "urgent"}
		resp.Fields.Project.Key = "PROJ"

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	jt := NewJiraTracker(server.URL, "PROJ", "user@example.com", "token123")

	issue, err := jt.GetIssue(context.Background(), "PROJ-123")
	if err != nil {
		t.Fatalf("GetIssue() error = %v", err)
	}

	if issue.ID != "PROJ-123" {
		t.Errorf("ID = %q, want %q", issue.ID, "PROJ-123")
	}
	if issue.Title != "Fix the bug" {
		t.Errorf("Title = %q, want %q", issue.Title, "Fix the bug")
	}
	if issue.Tracker != "jira" {
		t.Errorf("Tracker = %q, want %q", issue.Tracker, "jira")
	}
	if issue.Status != "in progress" {
		t.Errorf("Status = %q, want %q", issue.Status, "in progress")
	}
	if len(issue.Labels) != 2 {
		t.Errorf("Labels = %v, want 2 labels", issue.Labels)
	}
}

func TestJiraTracker_CreateIssue(t *testing.T) {
	var receivedBody jiraCreateRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/issue" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		json.NewDecoder(r.Body).Decode(&receivedBody)

		resp := jiraCreateResponse{
			ID:   "10002",
			Key:  "PROJ-124",
			Self: "https://jira.example.com/rest/api/3/issue/10002",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	jt := NewJiraTracker(server.URL, "PROJ", "user@example.com", "token123")

	issue, err := jt.CreateIssue(context.Background(), &CreateIssueRequest{
		Title:  "New feature",
		Body:   "Implement new feature",
		Labels: []string{"feature"},
	})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}

	if issue.ID != "PROJ-124" {
		t.Errorf("ID = %q, want %q", issue.ID, "PROJ-124")
	}

	// Verify request body.
	if receivedBody.Fields.Project.Key != "PROJ" {
		t.Errorf("Project = %q, want %q", receivedBody.Fields.Project.Key, "PROJ")
	}
	if receivedBody.Fields.Summary != "New feature" {
		t.Errorf("Summary = %q, want %q", receivedBody.Fields.Summary, "New feature")
	}
	if receivedBody.Fields.IssueType.Name != "Task" {
		t.Errorf("IssueType = %q, want %q", receivedBody.Fields.IssueType.Name, "Task")
	}
}

func TestJiraTracker_Comment(t *testing.T) {
	var receivedComment map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/issue/PROJ-123/comment" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		json.NewDecoder(r.Body).Decode(&receivedComment)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	jt := NewJiraTracker(server.URL, "PROJ", "user@example.com", "token123")

	err := jt.Comment(context.Background(), "PROJ-123", "Build complete")
	if err != nil {
		t.Fatalf("Comment() error = %v", err)
	}

	if receivedComment["body"] == nil {
		t.Error("comment body not sent")
	}
}

func TestJiraTracker_Link(t *testing.T) {
	var receivedLink jiraLinkRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/issueLink" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		json.NewDecoder(r.Body).Decode(&receivedLink)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	jt := NewJiraTracker(server.URL, "PROJ", "user@example.com", "token123")

	err := jt.Link(context.Background(), "PROJ-123", "PROJ-124", RelDependsOn)
	if err != nil {
		t.Fatalf("Link() error = %v", err)
	}

	if receivedLink.InwardIssue.Key != "PROJ-123" {
		t.Errorf("InwardIssue = %q, want %q", receivedLink.InwardIssue.Key, "PROJ-123")
	}
	if receivedLink.OutwardIssue.Key != "PROJ-124" {
		t.Errorf("OutwardIssue = %q, want %q", receivedLink.OutwardIssue.Key, "PROJ-124")
	}
	if receivedLink.Type.Name != "Blocks" {
		t.Errorf("Type = %q, want %q", receivedLink.Type.Name, "Blocks")
	}
}

func TestJiraTracker_CreatePR_NotSupported(t *testing.T) {
	jt := NewJiraTracker("https://jira.example.com", "PROJ", "user@example.com", "token123")
	_, err := jt.CreatePR(context.Background(), &CreatePRRequest{})
	if !errors.Is(err, ErrNotSupported) {
		t.Errorf("CreatePR() error = %v, want ErrNotSupported", err)
	}
}

func TestJiraTracker_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Unauthorized"}`))
	}))
	defer server.Close()

	jt := NewJiraTracker(server.URL, "PROJ", "user@example.com", "badtoken")

	_, err := jt.GetIssue(context.Background(), "PROJ-123")
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestJiraTracker_ResolveKey(t *testing.T) {
	jt := NewJiraTracker("https://jira.example.com", "PROJ", "", "")

	tests := []struct {
		ref  string
		want string
	}{
		{"PROJ-123", "PROJ-123"},
		{"jira:PROJ-123", "PROJ-123"},
		{"jira://PROJ-123", "PROJ-123"},
		{"123", "PROJ-123"},
	}

	for _, tt := range tests {
		got := jt.resolveKey(tt.ref)
		if got != tt.want {
			t.Errorf("resolveKey(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}
