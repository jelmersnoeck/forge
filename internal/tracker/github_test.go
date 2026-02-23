package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// mockGH captures args and returns predetermined output.
type mockGH struct {
	// capturedArgs stores the args from each call.
	capturedArgs [][]string
	// output to return.
	output []byte
	// err to return.
	err error
}

func (m *mockGH) run(_ context.Context, args ...string) ([]byte, error) {
	m.capturedArgs = append(m.capturedArgs, args)
	return m.output, m.err
}

// withMockGH replaces runGH for the duration of a test and restores it after.
func withMockGH(t *testing.T, m *mockGH) {
	t.Helper()
	orig := runGH
	runGH = m.run
	t.Cleanup(func() { runGH = orig })
}

func TestGitHubTracker_GetIssue(t *testing.T) {
	ghJSON := ghIssueJSON{
		Number: 42,
		Title:  "Test Issue",
		Body:   "This is the body.",
		State:  "OPEN",
		URL:    "https://github.com/myorg/myrepo/issues/42",
		Labels: []struct {
			Name string `json:"name"`
		}{
			{Name: "bug"},
			{Name: "priority:high"},
		},
	}
	data, _ := json.Marshal(ghJSON)

	m := &mockGH{output: data}
	withMockGH(t, m)

	tracker := NewGitHubTracker("myorg", "myrepo")
	issue, err := tracker.GetIssue(context.Background(), "42")
	if err != nil {
		t.Fatalf("GetIssue unexpected error: %v", err)
	}

	// Verify command construction.
	if len(m.capturedArgs) != 1 {
		t.Fatalf("expected 1 gh call, got %d", len(m.capturedArgs))
	}
	args := m.capturedArgs[0]
	wantArgs := []string{"issue", "view", "42", "--json", "number,title,body,labels,state,url", "--repo", "myorg/myrepo"}
	if len(args) != len(wantArgs) {
		t.Fatalf("args = %v, want %v", args, wantArgs)
	}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], wantArgs[i])
		}
	}

	// Verify parsed issue.
	if issue.ID != "42" {
		t.Errorf("ID = %q, want %q", issue.ID, "42")
	}
	if issue.Title != "Test Issue" {
		t.Errorf("Title = %q, want %q", issue.Title, "Test Issue")
	}
	if issue.Body != "This is the body." {
		t.Errorf("Body = %q, want %q", issue.Body, "This is the body.")
	}
	if issue.Status != "open" {
		t.Errorf("Status = %q, want %q", issue.Status, "open")
	}
	if issue.URL != "https://github.com/myorg/myrepo/issues/42" {
		t.Errorf("URL = %q", issue.URL)
	}
	if len(issue.Labels) != 2 || issue.Labels[0] != "bug" || issue.Labels[1] != "priority:high" {
		t.Errorf("Labels = %v, want [bug priority:high]", issue.Labels)
	}
	if issue.Tracker != "github" {
		t.Errorf("Tracker = %q, want %q", issue.Tracker, "github")
	}
	if issue.Repo != "myorg/myrepo" {
		t.Errorf("Repo = %q, want %q", issue.Repo, "myorg/myrepo")
	}
}

func TestGitHubTracker_GetIssue_Error(t *testing.T) {
	m := &mockGH{err: errors.New("gh issue view: not found")}
	withMockGH(t, m)

	tracker := NewGitHubTracker("myorg", "myrepo")
	_, err := tracker.GetIssue(context.Background(), "999")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "get issue 999") {
		t.Errorf("error should contain 'get issue 999', got: %v", err)
	}
}

func TestGitHubTracker_GetIssue_InvalidJSON(t *testing.T) {
	m := &mockGH{output: []byte("not json")}
	withMockGH(t, m)

	tracker := NewGitHubTracker("myorg", "myrepo")
	_, err := tracker.GetIssue(context.Background(), "42")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "parsing response") {
		t.Errorf("error should contain 'parsing response', got: %v", err)
	}
}

func TestGitHubTracker_CreateIssue(t *testing.T) {
	m := &mockGH{output: []byte("https://github.com/myorg/myrepo/issues/99\n")}
	withMockGH(t, m)

	tracker := NewGitHubTracker("myorg", "myrepo")
	issue, err := tracker.CreateIssue(context.Background(), &CreateIssueRequest{
		Title:  "New Issue",
		Body:   "Issue body",
		Labels: []string{"enhancement", "tracker"},
	})
	if err != nil {
		t.Fatalf("CreateIssue unexpected error: %v", err)
	}

	// Verify command construction.
	args := m.capturedArgs[0]
	wantArgs := []string{"issue", "create", "--title", "New Issue", "--body", "Issue body", "--repo", "myorg/myrepo", "--label", "enhancement", "--label", "tracker"}
	if len(args) != len(wantArgs) {
		t.Fatalf("args = %v, want %v", args, wantArgs)
	}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], wantArgs[i])
		}
	}

	if issue.URL != "https://github.com/myorg/myrepo/issues/99" {
		t.Errorf("URL = %q", issue.URL)
	}
	if issue.Title != "New Issue" {
		t.Errorf("Title = %q", issue.Title)
	}
}

func TestGitHubTracker_CreatePR(t *testing.T) {
	m := &mockGH{output: []byte("https://github.com/myorg/myrepo/pull/10\n")}
	withMockGH(t, m)

	tracker := NewGitHubTracker("myorg", "myrepo")
	pr, err := tracker.CreatePR(context.Background(), &CreatePRRequest{
		Title:     "My PR",
		Body:      "PR body",
		Head:      "feature-branch",
		Base:      "main",
		DraftMode: true,
		Labels:    []string{"wip"},
	})
	if err != nil {
		t.Fatalf("CreatePR unexpected error: %v", err)
	}

	args := m.capturedArgs[0]
	// Verify key args are present.
	joined := strings.Join(args, " ")
	for _, want := range []string{"pr", "create", "--title", "My PR", "--head", "feature-branch", "--base", "main", "--draft", "--label", "wip"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %v", want, args)
		}
	}

	if pr.URL != "https://github.com/myorg/myrepo/pull/10" {
		t.Errorf("URL = %q", pr.URL)
	}
}

func TestGitHubTracker_CreatePR_NoDraft(t *testing.T) {
	m := &mockGH{output: []byte("https://github.com/myorg/myrepo/pull/11\n")}
	withMockGH(t, m)

	tracker := NewGitHubTracker("myorg", "myrepo")
	_, err := tracker.CreatePR(context.Background(), &CreatePRRequest{
		Title:     "Non-draft PR",
		Body:      "body",
		Head:      "branch",
		Base:      "main",
		DraftMode: false,
	})
	if err != nil {
		t.Fatalf("CreatePR unexpected error: %v", err)
	}

	args := m.capturedArgs[0]
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--draft") {
		t.Errorf("--draft should not be present for non-draft PR: %v", args)
	}
}

func TestGitHubTracker_Comment(t *testing.T) {
	m := &mockGH{output: []byte("")}
	withMockGH(t, m)

	tracker := NewGitHubTracker("myorg", "myrepo")
	err := tracker.Comment(context.Background(), "42", "Build complete")
	if err != nil {
		t.Fatalf("Comment unexpected error: %v", err)
	}

	args := m.capturedArgs[0]
	wantArgs := []string{"issue", "comment", "42", "--body", "Build complete", "--repo", "myorg/myrepo"}
	if len(args) != len(wantArgs) {
		t.Fatalf("args = %v, want %v", args, wantArgs)
	}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], wantArgs[i])
		}
	}
}

func TestGitHubTracker_Comment_Error(t *testing.T) {
	m := &mockGH{err: errors.New("permission denied")}
	withMockGH(t, m)

	tracker := NewGitHubTracker("myorg", "myrepo")
	err := tracker.Comment(context.Background(), "42", "test")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "comment on 42") {
		t.Errorf("error should contain 'comment on 42', got: %v", err)
	}
}

func TestGitHubTracker_Link(t *testing.T) {
	m := &mockGH{output: []byte("")}
	withMockGH(t, m)

	tracker := NewGitHubTracker("myorg", "myrepo")
	err := tracker.Link(context.Background(), "10", "20", RelBlocks)
	if err != nil {
		t.Fatalf("Link unexpected error: %v", err)
	}

	args := m.capturedArgs[0]
	// Should be a comment on issue 10.
	if args[0] != "issue" || args[1] != "comment" || args[2] != "10" {
		t.Errorf("expected 'issue comment 10', got: %v", args[:3])
	}
	// Body should mention the relation and target issue.
	bodyIdx := -1
	for i, a := range args {
		if a == "--body" && i+1 < len(args) {
			bodyIdx = i + 1
			break
		}
	}
	if bodyIdx < 0 {
		t.Fatal("--body flag not found")
	}
	if !strings.Contains(args[bodyIdx], "blocks") || !strings.Contains(args[bodyIdx], "#20") {
		t.Errorf("body should mention 'blocks' and '#20', got: %q", args[bodyIdx])
	}
}

func TestGitHubTracker_Link_Error(t *testing.T) {
	m := &mockGH{err: errors.New("failed")}
	withMockGH(t, m)

	tracker := NewGitHubTracker("myorg", "myrepo")
	err := tracker.Link(context.Background(), "10", "20", RelDependsOn)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "link 10 -> 20") {
		t.Errorf("error should contain 'link 10 -> 20', got: %v", err)
	}
}
