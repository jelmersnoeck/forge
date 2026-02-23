package engine

import (
	"testing"

	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/principles"
	"github.com/jelmersnoeck/forge/internal/tracker"
)

func TestNew_Success(t *testing.T) {
	agents := map[string]agent.Agent{"mock": &mockAgent{}}
	trackers := map[string]tracker.Tracker{"github": newMockTracker()}

	eng, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "github",
	}, agents, trackers, principles.NewStore())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if eng == nil {
		t.Fatal("expected non-nil engine")
	}
	if eng.Config.MaxIterations != 3 {
		t.Errorf("expected default MaxIterations=3, got %d", eng.Config.MaxIterations)
	}
	if eng.Config.BranchPattern != "forge/{{.Tracker}}-{{.IssueID}}" {
		t.Errorf("unexpected default branch pattern: %s", eng.Config.BranchPattern)
	}
	if eng.Config.SeverityThreshold != "critical" {
		t.Errorf("unexpected default severity threshold: %s", eng.Config.SeverityThreshold)
	}
}

func TestNew_NilConfig(t *testing.T) {
	_, err := New(nil, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestNew_MissingAgent(t *testing.T) {
	agents := map[string]agent.Agent{"mock": &mockAgent{}}
	trackers := map[string]tracker.Tracker{"github": newMockTracker()}

	_, err := New(&EngineConfig{
		DefaultAgent:   "nonexistent",
		DefaultTracker: "github",
	}, agents, trackers, nil)
	if err == nil {
		t.Fatal("expected error for missing agent")
	}
}

func TestNew_MissingTracker(t *testing.T) {
	agents := map[string]agent.Agent{"mock": &mockAgent{}}
	trackers := map[string]tracker.Tracker{"github": newMockTracker()}

	_, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "nonexistent",
	}, agents, trackers, nil)
	if err == nil {
		t.Fatal("expected error for missing tracker")
	}
}

func TestNew_AgentRoleDefaults(t *testing.T) {
	agents := map[string]agent.Agent{"mock": &mockAgent{}}
	trackers := map[string]tracker.Tracker{"github": newMockTracker()}

	eng, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "github",
	}, agents, trackers, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if eng.Config.PlannerAgent != "mock" {
		t.Errorf("expected PlannerAgent to default to 'mock', got %q", eng.Config.PlannerAgent)
	}
	if eng.Config.CoderAgent != "mock" {
		t.Errorf("expected CoderAgent to default to 'mock', got %q", eng.Config.CoderAgent)
	}
	if eng.Config.ReviewerAgent != "mock" {
		t.Errorf("expected ReviewerAgent to default to 'mock', got %q", eng.Config.ReviewerAgent)
	}
}

func TestNew_CustomAgentRoles(t *testing.T) {
	agents := map[string]agent.Agent{
		"planner":  &mockAgent{},
		"coder":    &mockAgent{},
		"reviewer": &mockAgent{},
	}
	trackers := map[string]tracker.Tracker{"github": newMockTracker()}

	eng, err := New(&EngineConfig{
		DefaultAgent:   "planner",
		DefaultTracker: "github",
		PlannerAgent:   "planner",
		CoderAgent:     "coder",
		ReviewerAgent:  "reviewer",
	}, agents, trackers, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if eng.Config.PlannerAgent != "planner" {
		t.Errorf("expected PlannerAgent='planner', got %q", eng.Config.PlannerAgent)
	}
	if eng.Config.CoderAgent != "coder" {
		t.Errorf("expected CoderAgent='coder', got %q", eng.Config.CoderAgent)
	}
	if eng.Config.ReviewerAgent != "reviewer" {
		t.Errorf("expected ReviewerAgent='reviewer', got %q", eng.Config.ReviewerAgent)
	}
}

func TestNew_InvalidAgentRole(t *testing.T) {
	agents := map[string]agent.Agent{"mock": &mockAgent{}}
	trackers := map[string]tracker.Tracker{"github": newMockTracker()}

	_, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "github",
		PlannerAgent:   "nonexistent",
	}, agents, trackers, nil)
	if err == nil {
		t.Fatal("expected error for invalid planner agent")
	}
}

func TestNew_CustomMaxIterations(t *testing.T) {
	agents := map[string]agent.Agent{"mock": &mockAgent{}}
	trackers := map[string]tracker.Tracker{"github": newMockTracker()}

	eng, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "github",
		MaxIterations:  5,
	}, agents, trackers, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if eng.Config.MaxIterations != 5 {
		t.Errorf("expected MaxIterations=5, got %d", eng.Config.MaxIterations)
	}
}

func TestNew_EmptyAgentName(t *testing.T) {
	// If DefaultAgent is empty but no roles are set, all role names will
	// also be empty. This should be allowed (validation happens at usage time).
	agents := map[string]agent.Agent{"mock": &mockAgent{}}
	trackers := map[string]tracker.Tracker{}

	_, err := New(&EngineConfig{}, agents, trackers, nil)
	if err != nil {
		t.Fatalf("New with empty config: %v", err)
	}
}
