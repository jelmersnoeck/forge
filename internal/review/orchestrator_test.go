package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/principles"
)

// mockAgent implements agent.Agent for testing.
type mockAgent struct {
	output string
	err    error
	calls  atomic.Int32
}

func (m *mockAgent) Run(_ context.Context, _ agent.Request) (*agent.Response, error) {
	m.calls.Add(1)
	if m.err != nil {
		return nil, m.err
	}
	return &agent.Response{
		Output:   m.output,
		ExitCode: 0,
	}, nil
}

func newTestStore(t *testing.T) *principles.Store {
	t.Helper()
	store := principles.NewStore()

	dir := t.TempDir()
	data := []byte(`name: security
version: "1.0"
description: Security principles
principles:
  - id: sec-001
    category: security
    severity: critical
    title: No SQL injection
    description: Prevent SQL injection attacks
    check: Ensure parameterized queries
  - id: sec-002
    category: security
    severity: warning
    title: Auth checks
    description: Ensure authentication checks
    check: Verify auth middleware
`)
	if err := os.WriteFile(filepath.Join(dir, "security.yaml"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := store.LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	return store
}

func makeFindingsJSON(findings []Finding) string {
	data, _ := json.Marshal(findings)
	return string(data)
}

func TestOrchestratorReview(t *testing.T) {
	findings1 := []Finding{
		{File: "main.go", Line: 10, PrincipleID: "sec-001", Severity: principles.SeverityCritical, Message: "SQL injection"},
	}
	findings2 := []Finding{
		{File: "handler.go", Line: 20, PrincipleID: "sec-002", Severity: principles.SeverityWarning, Message: "Missing auth"},
	}

	agents := map[string]agent.Agent{
		"agent1": &mockAgent{output: makeFindingsJSON(findings1)},
		"agent2": &mockAgent{output: makeFindingsJSON(findings2)},
	}

	store := newTestStore(t)
	orch := NewOrchestrator(agents, store)

	req := ReviewRequest{
		Diff:          "--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new",
		PrincipleSets: []string{"security"},
		WorkDir:       t.TempDir(),
		Reviewers: []ReviewerConfig{
			{Name: "reviewer1", Agent: "agent1"},
			{Name: "reviewer2", Agent: "agent2"},
		},
	}

	result, err := orch.Review(context.Background(), req)
	if err != nil {
		t.Fatalf("Review() error: %v", err)
	}

	if len(result.Findings) != 2 {
		t.Errorf("expected 2 findings, got %d", len(result.Findings))
	}
	if !result.HasCritical {
		t.Error("expected HasCritical=true")
	}
}

func TestOrchestratorReviewParallelExecution(t *testing.T) {
	agent1 := &mockAgent{output: "[]"}
	agent2 := &mockAgent{output: "[]"}

	agents := map[string]agent.Agent{
		"agent1": agent1,
		"agent2": agent2,
	}

	store := newTestStore(t)
	orch := NewOrchestrator(agents, store)

	req := ReviewRequest{
		Diff:          "diff content",
		PrincipleSets: []string{"security"},
		WorkDir:       t.TempDir(),
		Reviewers: []ReviewerConfig{
			{Name: "r1", Agent: "agent1"},
			{Name: "r2", Agent: "agent2"},
		},
	}

	_, err := orch.Review(context.Background(), req)
	if err != nil {
		t.Fatalf("Review() error: %v", err)
	}

	if agent1.calls.Load() != 1 {
		t.Errorf("agent1 called %d times, want 1", agent1.calls.Load())
	}
	if agent2.calls.Load() != 1 {
		t.Errorf("agent2 called %d times, want 1", agent2.calls.Load())
	}
}

func TestOrchestratorReviewDeduplicates(t *testing.T) {
	// Both agents return the same finding.
	sameFinding := []Finding{
		{File: "main.go", Line: 10, PrincipleID: "sec-001", Severity: principles.SeverityCritical, Message: "issue"},
	}

	agents := map[string]agent.Agent{
		"agent1": &mockAgent{output: makeFindingsJSON(sameFinding)},
		"agent2": &mockAgent{output: makeFindingsJSON(sameFinding)},
	}

	store := newTestStore(t)
	orch := NewOrchestrator(agents, store)

	req := ReviewRequest{
		Diff:          "diff content",
		PrincipleSets: []string{"security"},
		WorkDir:       t.TempDir(),
		Reviewers: []ReviewerConfig{
			{Name: "r1", Agent: "agent1"},
			{Name: "r2", Agent: "agent2"},
		},
	}

	result, err := orch.Review(context.Background(), req)
	if err != nil {
		t.Fatalf("Review() error: %v", err)
	}

	if len(result.Findings) != 1 {
		t.Errorf("expected 1 deduplicated finding, got %d", len(result.Findings))
	}
}

func TestOrchestratorReviewNoPrincipleSets(t *testing.T) {
	agents := map[string]agent.Agent{
		"agent1": &mockAgent{output: "[]"},
	}

	store := newTestStore(t)
	orch := NewOrchestrator(agents, store)

	req := ReviewRequest{
		Diff:    "diff content",
		WorkDir: t.TempDir(),
		Reviewers: []ReviewerConfig{
			{Name: "r1", Agent: "agent1"},
		},
	}

	_, err := orch.Review(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for empty principle sets")
	}
}

func TestOrchestratorReviewNoReviewers(t *testing.T) {
	agents := map[string]agent.Agent{
		"agent1": &mockAgent{output: "[]"},
	}

	store := newTestStore(t)
	orch := NewOrchestrator(agents, store)

	req := ReviewRequest{
		Diff:          "diff content",
		PrincipleSets: []string{"security"},
		WorkDir:       t.TempDir(),
		Reviewers:     nil,
	}

	_, err := orch.Review(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for no reviewers")
	}
}

func TestOrchestratorReviewAgentNotFound(t *testing.T) {
	agents := map[string]agent.Agent{
		"agent1": &mockAgent{output: "[]"},
	}

	store := newTestStore(t)
	orch := NewOrchestrator(agents, store)

	req := ReviewRequest{
		Diff:          "diff content",
		PrincipleSets: []string{"security"},
		WorkDir:       t.TempDir(),
		Reviewers: []ReviewerConfig{
			{Name: "r1", Agent: "nonexistent"},
		},
	}

	_, err := orch.Review(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when all reviewers fail")
	}
}

func TestOrchestratorReviewPartialFailure(t *testing.T) {
	findings := []Finding{
		{File: "main.go", Line: 10, PrincipleID: "sec-001", Severity: principles.SeverityWarning, Message: "issue"},
	}

	agents := map[string]agent.Agent{
		"good":   &mockAgent{output: makeFindingsJSON(findings)},
		"broken": &mockAgent{err: fmt.Errorf("agent crashed")},
	}

	store := newTestStore(t)
	orch := NewOrchestrator(agents, store)

	req := ReviewRequest{
		Diff:          "diff content",
		PrincipleSets: []string{"security"},
		WorkDir:       t.TempDir(),
		Reviewers: []ReviewerConfig{
			{Name: "good-reviewer", Agent: "good"},
			{Name: "broken-reviewer", Agent: "broken"},
		},
	}

	result, err := orch.Review(context.Background(), req)
	if err != nil {
		t.Fatalf("expected partial success, got error: %v", err)
	}

	if len(result.Findings) != 1 {
		t.Errorf("expected 1 finding from successful reviewer, got %d", len(result.Findings))
	}
}

func TestOrchestratorReviewerSpecificPrincipleSets(t *testing.T) {
	agent1 := &mockAgent{output: "[]"}

	agents := map[string]agent.Agent{
		"agent1": agent1,
	}

	store := newTestStore(t)
	orch := NewOrchestrator(agents, store)

	req := ReviewRequest{
		Diff:          "diff content",
		PrincipleSets: []string{"security"},
		WorkDir:       t.TempDir(),
		Reviewers: []ReviewerConfig{
			{Name: "r1", Agent: "agent1", PrincipleSets: []string{"security"}},
		},
	}

	_, err := orch.Review(context.Background(), req)
	if err != nil {
		t.Fatalf("Review() error: %v", err)
	}

	if agent1.calls.Load() != 1 {
		t.Errorf("agent1 called %d times, want 1", agent1.calls.Load())
	}
}
