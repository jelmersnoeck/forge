package planner

import (
	"context"
	"fmt"
	"testing"

	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/tracker"
)

// mockAgent implements agent.Agent for testing.
type mockAgent struct {
	output string
	err    error
}

func (m *mockAgent) Run(ctx context.Context, req agent.Request) (*agent.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &agent.Response{
		Output: m.output,
	}, nil
}

// mockTracker implements tracker.Tracker for testing.
type mockTracker struct {
	issues  []*tracker.Issue
	created []*tracker.CreateIssueRequest
	links   []linkCall
}

type linkCall struct {
	from string
	to   string
	rel  tracker.LinkRelation
}

func (m *mockTracker) GetIssue(ctx context.Context, ref string) (*tracker.Issue, error) {
	return &tracker.Issue{ID: ref, Title: "Mock Issue"}, nil
}

func (m *mockTracker) CreateIssue(ctx context.Context, req *tracker.CreateIssueRequest) (*tracker.Issue, error) {
	m.created = append(m.created, req)
	id := fmt.Sprintf("#%d", len(m.created))
	return &tracker.Issue{
		ID:    id,
		Title: req.Title,
		Body:  req.Body,
		URL:   "https://example.com/issues/" + id,
	}, nil
}

func (m *mockTracker) CreatePR(ctx context.Context, req *tracker.CreatePRRequest) (*tracker.PullRequest, error) {
	return nil, tracker.ErrNotSupported
}

func (m *mockTracker) Comment(ctx context.Context, ref string, body string) error {
	return nil
}

func (m *mockTracker) Link(ctx context.Context, from string, to string, rel tracker.LinkRelation) error {
	m.links = append(m.links, linkCall{from, to, rel})
	return nil
}

func TestPlan_Success(t *testing.T) {
	agentOutput := `Here is the plan:

` + "```yaml" + `
id: ws-auth
goal: "Implement authentication"
phases:
  - name: "Foundation"
    issues:
      - title: "Add user model"
        description: "Create User struct"
        labels:
          - feature
      - title: "Add auth middleware"
        description: "JWT middleware"
        depends_on:
          - "Add user model"
` + "```" + `

This plan covers the auth system.`

	ma := &mockAgent{output: agentOutput}
	mt := &mockTracker{}
	p := New(ma, mt)

	ws, err := p.Plan(context.Background(), PlanRequest{
		Goal:    "Implement authentication",
		Tracker: "github",
		Repo:    "org/repo",
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	if ws.ID != "ws-auth" {
		t.Errorf("ID = %q, want %q", ws.ID, "ws-auth")
	}
	if ws.Goal != "Implement authentication" {
		t.Errorf("Goal = %q, want %q", ws.Goal, "Implement authentication")
	}
	if ws.Tracker != "github" {
		t.Errorf("Tracker = %q, want %q", ws.Tracker, "github")
	}
	if ws.Repo != "org/repo" {
		t.Errorf("Repo = %q, want %q", ws.Repo, "org/repo")
	}
	if len(ws.Phases) != 1 {
		t.Fatalf("Phases = %d, want 1", len(ws.Phases))
	}
	if len(ws.Phases[0].Issues) != 2 {
		t.Fatalf("Issues = %d, want 2", len(ws.Phases[0].Issues))
	}
	if ws.Status != StatusPending {
		t.Errorf("Status = %q, want %q", ws.Status, StatusPending)
	}
}

func TestPlan_EmptyGoal(t *testing.T) {
	ma := &mockAgent{}
	mt := &mockTracker{}
	p := New(ma, mt)

	_, err := p.Plan(context.Background(), PlanRequest{Goal: ""})
	if err == nil {
		t.Fatal("expected error for empty goal")
	}
}

func TestPlan_AgentError(t *testing.T) {
	ma := &mockAgent{err: fmt.Errorf("agent failed")}
	mt := &mockTracker{}
	p := New(ma, mt)

	_, err := p.Plan(context.Background(), PlanRequest{Goal: "test"})
	if err == nil {
		t.Fatal("expected error when agent fails")
	}
}

func TestPlan_AgentResponseError(t *testing.T) {
	// mockAgent not used in this test, we use errorAgent directly.
	// Need to override to return an error response.
	p := New(&errorAgent{}, &mockTracker{})

	_, err := p.Plan(context.Background(), PlanRequest{Goal: "test"})
	if err == nil {
		t.Fatal("expected error when agent returns error response")
	}
}

type errorAgent struct{}

func (e *errorAgent) Run(ctx context.Context, req agent.Request) (*agent.Response, error) {
	return &agent.Response{Error: "something went wrong"}, nil
}

func TestPlan_InvalidYAML(t *testing.T) {
	ma := &mockAgent{output: "not yaml at all {{{"}
	mt := &mockTracker{}
	p := New(ma, mt)

	_, err := p.Plan(context.Background(), PlanRequest{Goal: "test"})
	if err == nil {
		t.Fatal("expected error for invalid YAML output")
	}
}

func TestPlan_MissingFields(t *testing.T) {
	// YAML that parses but has no ID.
	ma := &mockAgent{output: "goal: test\nphases: []"}
	mt := &mockTracker{}
	p := New(ma, mt)

	_, err := p.Plan(context.Background(), PlanRequest{Goal: "test"})
	if err == nil {
		t.Fatal("expected error for missing id field")
	}
}

func TestExtractYAMLBlock(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "with yaml fence",
			input: "Some text\n```yaml\nid: test\n```\nMore text",
			want:  "id: test",
		},
		{
			name:  "with yml fence",
			input: "Some text\n```yml\nid: test\n```\nMore text",
			want:  "id: test",
		},
		{
			name:  "no fence",
			input: "id: test\ngoal: test",
			want:  "",
		},
		{
			name:  "empty block",
			input: "```yaml\n```",
			want:  "",
		},
		{
			name:  "multiline",
			input: "```yaml\nid: test\ngoal: test\nphases:\n  - name: P1\n```",
			want:  "id: test\ngoal: test\nphases:\n  - name: P1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractYAMLBlock(tt.input)
			if got != tt.want {
				t.Errorf("extractYAMLBlock() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCreateIssues(t *testing.T) {
	ma := &mockAgent{}
	mt := &mockTracker{}
	p := New(ma, mt)

	ws := &Workstream{
		ID:      "ws-test",
		Goal:    "Test",
		Tracker: "github",
		Repo:    "org/repo",
		Status:  StatusPending,
		Phases: []Phase{
			{
				Name: "Phase 1",
				Issues: []WorkstreamIssue{
					{Title: "Issue A", Description: "Desc A", Labels: []string{"feature"}, Status: StatusPending},
					{Title: "Issue B", Description: "Desc B", DependsOn: []string{"Issue A"}, Status: StatusPending},
				},
			},
		},
	}

	if err := p.CreateIssues(context.Background(), ws); err != nil {
		t.Fatalf("CreateIssues() error = %v", err)
	}

	// Check that issues were created.
	if len(mt.created) != 2 {
		t.Fatalf("created = %d, want 2", len(mt.created))
	}

	// Check that "forge" label was added.
	hasForge := false
	for _, l := range mt.created[0].Labels {
		if l == "forge" {
			hasForge = true
		}
	}
	if !hasForge {
		t.Error("first issue missing 'forge' label")
	}

	// Check that refs were set.
	if ws.Phases[0].Issues[0].Ref == "" {
		t.Error("Issue A ref not set")
	}
	if ws.Phases[0].Issues[1].Ref == "" {
		t.Error("Issue B ref not set")
	}

	// Check that dependency link was created.
	if len(mt.links) != 1 {
		t.Fatalf("links = %d, want 1", len(mt.links))
	}
	if mt.links[0].rel != tracker.RelDependsOn {
		t.Errorf("link rel = %q, want %q", mt.links[0].rel, tracker.RelDependsOn)
	}
}
