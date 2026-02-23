package engine

import (
	"context"
	"fmt"

	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/tracker"
)

// mockAgent implements agent.Agent for testing.
type mockAgent struct {
	// RunFunc is called when Run is invoked. If nil, a default response is returned.
	RunFunc func(ctx context.Context, req agent.Request) (*agent.Response, error)
	// Calls records all Run invocations.
	Calls []agent.Request
}

func (m *mockAgent) Run(ctx context.Context, req agent.Request) (*agent.Response, error) {
	m.Calls = append(m.Calls, req)
	if m.RunFunc != nil {
		return m.RunFunc(ctx, req)
	}
	return &agent.Response{
		Output:   "mock output",
		ExitCode: 0,
	}, nil
}

// mockTracker implements tracker.Tracker for testing.
type mockTracker struct {
	Issues map[string]*tracker.Issue
	PRs    []*tracker.PullRequest

	GetIssueFunc   func(ctx context.Context, ref string) (*tracker.Issue, error)
	CreatePRFunc   func(ctx context.Context, req *tracker.CreatePRRequest) (*tracker.PullRequest, error)
	CreateIssueFunc func(ctx context.Context, req *tracker.CreateIssueRequest) (*tracker.Issue, error)
	CommentFunc    func(ctx context.Context, ref string, body string) error
	LinkFunc       func(ctx context.Context, from string, to string, rel tracker.LinkRelation) error
}

func newMockTracker() *mockTracker {
	return &mockTracker{
		Issues: make(map[string]*tracker.Issue),
	}
}

func (m *mockTracker) GetIssue(ctx context.Context, ref string) (*tracker.Issue, error) {
	if m.GetIssueFunc != nil {
		return m.GetIssueFunc(ctx, ref)
	}
	issue, ok := m.Issues[ref]
	if !ok {
		return nil, fmt.Errorf("issue %q not found", ref)
	}
	return issue, nil
}

func (m *mockTracker) CreateIssue(ctx context.Context, req *tracker.CreateIssueRequest) (*tracker.Issue, error) {
	if m.CreateIssueFunc != nil {
		return m.CreateIssueFunc(ctx, req)
	}
	return &tracker.Issue{ID: "new-1", Title: req.Title, Body: req.Body}, nil
}

func (m *mockTracker) CreatePR(ctx context.Context, req *tracker.CreatePRRequest) (*tracker.PullRequest, error) {
	if m.CreatePRFunc != nil {
		return m.CreatePRFunc(ctx, req)
	}
	pr := &tracker.PullRequest{
		ID:     "pr-1",
		Number: 1,
		URL:    "https://github.com/test/repo/pull/1",
		Status: "open",
	}
	m.PRs = append(m.PRs, pr)
	return pr, nil
}

func (m *mockTracker) Comment(ctx context.Context, ref string, body string) error {
	if m.CommentFunc != nil {
		return m.CommentFunc(ctx, ref, body)
	}
	return nil
}

func (m *mockTracker) Link(ctx context.Context, from string, to string, rel tracker.LinkRelation) error {
	if m.LinkFunc != nil {
		return m.LinkFunc(ctx, from, to, rel)
	}
	return nil
}
