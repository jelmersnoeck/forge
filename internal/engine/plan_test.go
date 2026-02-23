package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/principles"
	"github.com/jelmersnoeck/forge/internal/tracker"
)

func TestPlan_Success(t *testing.T) {
	ma := &mockAgent{
		RunFunc: func(_ context.Context, req agent.Request) (*agent.Response, error) {
			if req.Mode != agent.ModePlan {
				t.Errorf("expected mode plan, got %s", req.Mode)
			}
			if !req.Permissions.Read {
				t.Error("expected read permission")
			}
			if req.Permissions.Write {
				t.Error("expected no write permission for plan")
			}
			return &agent.Response{
				Output: "1. Create file\n2. Implement logic\n3. Write tests",
			}, nil
		},
	}

	mt := newMockTracker()
	mt.Issues["gh:test/repo#42"] = &tracker.Issue{
		ID:    "42",
		Title: "Add feature X",
		Body:  "Implement the new feature X",
	}

	eng, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "github",
	}, map[string]agent.Agent{"mock": ma}, map[string]tracker.Tracker{"github": mt}, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	result, err := eng.Plan(context.Background(), PlanRequest{
		IssueRef: "gh:test/repo#42",
		WorkDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if result.Plan == "" {
		t.Error("expected non-empty plan")
	}
	if result.Approved {
		t.Error("expected plan not to be auto-approved")
	}
	if len(ma.Calls) != 1 {
		t.Errorf("expected 1 agent call, got %d", len(ma.Calls))
	}
}

func TestPlan_WithPrinciples(t *testing.T) {
	var capturedPrompt string
	ma := &mockAgent{
		RunFunc: func(_ context.Context, req agent.Request) (*agent.Response, error) {
			capturedPrompt = req.Prompt
			return &agent.Response{Output: "plan with principles"}, nil
		},
	}

	mt := newMockTracker()
	mt.Issues["gh:test/repo#1"] = &tracker.Issue{
		ID:    "1",
		Title: "Secure endpoint",
		Body:  "Add auth to the API",
	}

	store := principles.NewStore()
	// We need to add principles to the store manually for testing.
	// The store's internal sets are unexported, so we test through the public API.
	// For this test, we verify the code path handles empty principle sets gracefully.

	eng, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "github",
	}, map[string]agent.Agent{"mock": ma}, map[string]tracker.Tracker{"github": mt}, store)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	// Request with a principle set that doesn't exist should error.
	_, err = eng.Plan(context.Background(), PlanRequest{
		IssueRef:      "gh:test/repo#1",
		PrincipleSets: []string{"nonexistent"},
		WorkDir:       t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for missing principle set")
	}

	// Without principle sets, it should succeed.
	result, err := eng.Plan(context.Background(), PlanRequest{
		IssueRef: "gh:test/repo#1",
		WorkDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Plan without principles: %v", err)
	}
	if result.Plan == "" {
		t.Error("expected plan output")
	}

	// Verify the prompt contains the issue details.
	if !strings.Contains(capturedPrompt, "Secure endpoint") {
		t.Error("expected prompt to contain issue title")
	}
}

func TestPlan_InvalidIssueRef(t *testing.T) {
	eng, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "github",
	}, map[string]agent.Agent{"mock": &mockAgent{}}, map[string]tracker.Tracker{"github": newMockTracker()}, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	_, err = eng.Plan(context.Background(), PlanRequest{
		IssueRef: "",
		WorkDir:  t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for empty issue ref")
	}
}

func TestPlan_TrackerNotConfigured(t *testing.T) {
	eng, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "github",
	}, map[string]agent.Agent{"mock": &mockAgent{}}, map[string]tracker.Tracker{"github": newMockTracker()}, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	// Use a jira ref but no jira tracker configured.
	_, err = eng.Plan(context.Background(), PlanRequest{
		IssueRef: "jira:PROJ-123",
		WorkDir:  t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for unconfigured tracker")
	}
}

func TestPlan_AgentError(t *testing.T) {
	ma := &mockAgent{
		RunFunc: func(_ context.Context, _ agent.Request) (*agent.Response, error) {
			return &agent.Response{Error: "model overloaded"}, nil
		},
	}

	mt := newMockTracker()
	mt.Issues["gh:test/repo#1"] = &tracker.Issue{
		ID:    "1",
		Title: "Test",
		Body:  "Test body",
	}

	eng, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "github",
	}, map[string]agent.Agent{"mock": ma}, map[string]tracker.Tracker{"github": mt}, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	_, err = eng.Plan(context.Background(), PlanRequest{
		IssueRef: "gh:test/repo#1",
		WorkDir:  t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for agent error response")
	}
}
