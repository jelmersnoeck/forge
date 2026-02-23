package planner

import (
	"sort"
	"testing"
)

// newTestWorkstream creates a test workstream with the given dependency structure.
func newTestWorkstream(issues []WorkstreamIssue) *Workstream {
	return &Workstream{
		ID:   "ws-test",
		Goal: "Test",
		Phases: []Phase{
			{Name: "All", Issues: issues},
		},
	}
}

func TestBuildGraph_Simple(t *testing.T) {
	ws := newTestWorkstream([]WorkstreamIssue{
		{Title: "A", Status: StatusPending},
		{Title: "B", Status: StatusPending, DependsOn: []string{"A"}},
		{Title: "C", Status: StatusPending, DependsOn: []string{"A"}},
		{Title: "D", Status: StatusPending, DependsOn: []string{"B", "C"}},
	})

	g, err := BuildGraph(ws)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	if g.HasCycle() {
		t.Error("HasCycle() = true, want false")
	}
	if g.IsComplete() {
		t.Error("IsComplete() = true, want false")
	}
}

func TestBuildGraph_DuplicateID(t *testing.T) {
	ws := newTestWorkstream([]WorkstreamIssue{
		{Title: "A", Status: StatusPending},
		{Title: "A", Status: StatusPending},
	})

	_, err := BuildGraph(ws)
	if err == nil {
		t.Fatal("expected error for duplicate IDs")
	}
}

func TestBuildGraph_UnknownDependency(t *testing.T) {
	ws := newTestWorkstream([]WorkstreamIssue{
		{Title: "A", Status: StatusPending, DependsOn: []string{"nonexistent"}},
	})

	_, err := BuildGraph(ws)
	if err == nil {
		t.Fatal("expected error for unknown dependency")
	}
}

func TestReady_NoDeps(t *testing.T) {
	ws := newTestWorkstream([]WorkstreamIssue{
		{Title: "A", Status: StatusPending},
		{Title: "B", Status: StatusPending},
	})

	g, err := BuildGraph(ws)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	ready := g.Ready()
	if len(ready) != 2 {
		t.Fatalf("Ready() = %d, want 2", len(ready))
	}
}

func TestReady_WithDeps(t *testing.T) {
	ws := newTestWorkstream([]WorkstreamIssue{
		{Title: "A", Status: StatusPending},
		{Title: "B", Status: StatusPending, DependsOn: []string{"A"}},
	})

	g, err := BuildGraph(ws)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	ready := g.Ready()
	if len(ready) != 1 {
		t.Fatalf("Ready() = %d, want 1", len(ready))
	}
	if ready[0].Title != "A" {
		t.Errorf("Ready()[0] = %q, want %q", ready[0].Title, "A")
	}

	// Complete A, then B should be ready.
	g.MarkComplete("A")
	ready = g.Ready()
	if len(ready) != 1 {
		t.Fatalf("Ready() after completing A = %d, want 1", len(ready))
	}
	if ready[0].Title != "B" {
		t.Errorf("Ready()[0] = %q, want %q", ready[0].Title, "B")
	}
}

func TestReady_AlreadyCompleted(t *testing.T) {
	ws := newTestWorkstream([]WorkstreamIssue{
		{Title: "A", Status: StatusCompleted},
		{Title: "B", Status: StatusPending, DependsOn: []string{"A"}},
	})

	g, err := BuildGraph(ws)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	ready := g.Ready()
	if len(ready) != 1 {
		t.Fatalf("Ready() = %d, want 1", len(ready))
	}
	if ready[0].Title != "B" {
		t.Errorf("Ready()[0] = %q, want %q", ready[0].Title, "B")
	}
}

func TestMarkComplete(t *testing.T) {
	ws := newTestWorkstream([]WorkstreamIssue{
		{Title: "A", Status: StatusPending},
	})

	g, err := BuildGraph(ws)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	if g.IsComplete() {
		t.Error("IsComplete() = true before marking")
	}

	g.MarkComplete("A")

	if !g.IsComplete() {
		t.Error("IsComplete() = false after marking A complete")
	}
}

func TestMarkFailed(t *testing.T) {
	ws := newTestWorkstream([]WorkstreamIssue{
		{Title: "A", Status: StatusPending},
		{Title: "B", Status: StatusPending, DependsOn: []string{"A"}},
	})

	g, err := BuildGraph(ws)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	g.MarkFailed("A")

	// A is treated as complete (failed).
	ready := g.Ready()
	// B's dependency (A) is "complete" (failed), so B appears ready.
	if len(ready) != 1 {
		t.Fatalf("Ready() after failing A = %d, want 1", len(ready))
	}
}

func TestHasCycle_True(t *testing.T) {
	ws := newTestWorkstream([]WorkstreamIssue{
		{Title: "A", Status: StatusPending, DependsOn: []string{"C"}},
		{Title: "B", Status: StatusPending, DependsOn: []string{"A"}},
		{Title: "C", Status: StatusPending, DependsOn: []string{"B"}},
	})

	g, err := BuildGraph(ws)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	if !g.HasCycle() {
		t.Error("HasCycle() = false, want true")
	}
}

func TestHasCycle_False(t *testing.T) {
	ws := newTestWorkstream([]WorkstreamIssue{
		{Title: "A", Status: StatusPending},
		{Title: "B", Status: StatusPending, DependsOn: []string{"A"}},
		{Title: "C", Status: StatusPending, DependsOn: []string{"B"}},
	})

	g, err := BuildGraph(ws)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	if g.HasCycle() {
		t.Error("HasCycle() = true, want false")
	}
}

func TestTopologicalSort_Linear(t *testing.T) {
	ws := newTestWorkstream([]WorkstreamIssue{
		{Title: "C", Status: StatusPending, DependsOn: []string{"B"}},
		{Title: "A", Status: StatusPending},
		{Title: "B", Status: StatusPending, DependsOn: []string{"A"}},
	})

	g, err := BuildGraph(ws)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	sorted, err := g.TopologicalSort()
	if err != nil {
		t.Fatalf("TopologicalSort() error = %v", err)
	}

	if len(sorted) != 3 {
		t.Fatalf("TopologicalSort() = %d items, want 3", len(sorted))
	}

	// Build position map to verify ordering.
	pos := make(map[string]int)
	for i, issue := range sorted {
		pos[issue.Title] = i
	}

	if pos["A"] >= pos["B"] {
		t.Errorf("A (pos %d) should come before B (pos %d)", pos["A"], pos["B"])
	}
	if pos["B"] >= pos["C"] {
		t.Errorf("B (pos %d) should come before C (pos %d)", pos["B"], pos["C"])
	}
}

func TestTopologicalSort_Diamond(t *testing.T) {
	ws := newTestWorkstream([]WorkstreamIssue{
		{Title: "A", Status: StatusPending},
		{Title: "B", Status: StatusPending, DependsOn: []string{"A"}},
		{Title: "C", Status: StatusPending, DependsOn: []string{"A"}},
		{Title: "D", Status: StatusPending, DependsOn: []string{"B", "C"}},
	})

	g, err := BuildGraph(ws)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	sorted, err := g.TopologicalSort()
	if err != nil {
		t.Fatalf("TopologicalSort() error = %v", err)
	}

	pos := make(map[string]int)
	for i, issue := range sorted {
		pos[issue.Title] = i
	}

	if pos["A"] >= pos["B"] || pos["A"] >= pos["C"] {
		t.Errorf("A should come before B and C")
	}
	if pos["B"] >= pos["D"] || pos["C"] >= pos["D"] {
		t.Errorf("B and C should come before D")
	}
}

func TestTopologicalSort_Cycle(t *testing.T) {
	ws := newTestWorkstream([]WorkstreamIssue{
		{Title: "A", Status: StatusPending, DependsOn: []string{"B"}},
		{Title: "B", Status: StatusPending, DependsOn: []string{"A"}},
	})

	g, err := BuildGraph(ws)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	_, err = g.TopologicalSort()
	if err == nil {
		t.Fatal("expected error for cyclic graph")
	}
}

func TestDependents(t *testing.T) {
	ws := newTestWorkstream([]WorkstreamIssue{
		{Title: "A", Status: StatusPending},
		{Title: "B", Status: StatusPending, DependsOn: []string{"A"}},
		{Title: "C", Status: StatusPending, DependsOn: []string{"A"}},
	})

	g, err := BuildGraph(ws)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	deps := g.Dependents("A")
	sort.Strings(deps)
	if len(deps) != 2 {
		t.Fatalf("Dependents(A) = %d, want 2", len(deps))
	}
	if deps[0] != "B" || deps[1] != "C" {
		t.Errorf("Dependents(A) = %v, want [B, C]", deps)
	}
}

func TestTransitiveDependents(t *testing.T) {
	ws := newTestWorkstream([]WorkstreamIssue{
		{Title: "A", Status: StatusPending},
		{Title: "B", Status: StatusPending, DependsOn: []string{"A"}},
		{Title: "C", Status: StatusPending, DependsOn: []string{"B"}},
		{Title: "D", Status: StatusPending, DependsOn: []string{"C"}},
	})

	g, err := BuildGraph(ws)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	deps := g.TransitiveDependents("A")
	sort.Strings(deps)
	if len(deps) != 3 {
		t.Fatalf("TransitiveDependents(A) = %d, want 3", len(deps))
	}
	if deps[0] != "B" || deps[1] != "C" || deps[2] != "D" {
		t.Errorf("TransitiveDependents(A) = %v, want [B, C, D]", deps)
	}
}

func TestIsComplete_AllDone(t *testing.T) {
	ws := newTestWorkstream([]WorkstreamIssue{
		{Title: "A", Status: StatusCompleted},
		{Title: "B", Status: StatusCompleted, DependsOn: []string{"A"}},
	})

	g, err := BuildGraph(ws)
	if err != nil {
		t.Fatalf("BuildGraph() error = %v", err)
	}

	if !g.IsComplete() {
		t.Error("IsComplete() = false, want true for all completed issues")
	}
}
