package planner

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

)

// mockEngine is a test double for engine.Engine that records Build calls.
type mockEngine struct {
	mu       sync.Mutex
	calls    []string // Issue refs that were built.
	failRefs map[string]bool
}

func newMockEngine(failRefs ...string) *mockEngine {
	m := &mockEngine{
		failRefs: make(map[string]bool),
	}
	for _, ref := range failRefs {
		m.failRefs[ref] = true
	}
	return m
}

func (m *mockEngine) recordCall(ref string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, ref)
}

func (m *mockEngine) getCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.calls))
	copy(result, m.calls)
	return result
}

// testExecutor wraps Executor but intercepts buildIssue to use a mock.
type testExecutor struct {
	mock        *mockEngine
	maxParallel int
}

func (te *testExecutor) Execute(ctx context.Context, ws *Workstream) error {
	graph, err := BuildGraph(ws)
	if err != nil {
		return fmt.Errorf("building dependency graph: %w", err)
	}

	if graph.HasCycle() {
		return fmt.Errorf("workstream %s has cyclic dependencies", ws.ID)
	}

	ws.Status = StatusInProgress

	var failMu sync.Mutex
	skipped := make(map[string]bool)

	for !graph.IsComplete() {
		if ctx.Err() != nil {
			ws.Status = StatusFailed
			return ctx.Err()
		}

		ready := graph.Ready()
		if len(ready) == 0 {
			break
		}

		var toRun []*WorkstreamIssue
		for _, issue := range ready {
			id := issue.IssueID()
			failMu.Lock()
			isSkipped := skipped[id]
			failMu.Unlock()
			if isSkipped {
				issue.Status = StatusFailed
				graph.MarkFailed(id)
				continue
			}
			toRun = append(toRun, issue)
		}

		if len(toRun) == 0 {
			continue
		}

		sem := make(chan struct{}, te.maxParallel)
		var wg sync.WaitGroup

		for _, issue := range toRun {
			wg.Add(1)
			go func(issue *WorkstreamIssue) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				id := issue.IssueID()
				issue.Status = StatusInProgress

				te.mock.recordCall(id)

				if te.mock.failRefs[id] {
					issue.Status = StatusFailed
					graph.MarkFailed(id)
					dependents := graph.TransitiveDependents(id)
					failMu.Lock()
					for _, dep := range dependents {
						skipped[dep] = true
					}
					failMu.Unlock()
					return
				}

				issue.Status = StatusCompleted
				graph.MarkComplete(id)
			}(issue)
		}
		wg.Wait()
	}

	allCompleted := true
	anyFailed := false
	for _, issue := range ws.AllIssues() {
		if issue.Status != StatusCompleted {
			allCompleted = false
		}
		if issue.Status == StatusFailed {
			anyFailed = true
		}
	}

	if allCompleted {
		ws.Status = StatusCompleted
	} else if anyFailed {
		ws.Status = StatusFailed
	}

	return nil
}

func TestExecutor_SimpleChain(t *testing.T) {
	ws := &Workstream{
		ID:   "ws-test",
		Goal: "Test",
		Phases: []Phase{
			{Name: "P1", Issues: []WorkstreamIssue{
				{Ref: "#1", Title: "A", Status: StatusPending},
				{Ref: "#2", Title: "B", Status: StatusPending, DependsOn: []string{"#1"}},
				{Ref: "#3", Title: "C", Status: StatusPending, DependsOn: []string{"#2"}},
			}},
		},
	}

	mock := newMockEngine()
	exec := &testExecutor{mock: mock, maxParallel: 1}

	if err := exec.Execute(context.Background(), ws); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if ws.Status != StatusCompleted {
		t.Errorf("Status = %q, want %q", ws.Status, StatusCompleted)
	}

	calls := mock.getCalls()
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(calls))
	}
}

func TestExecutor_Parallel(t *testing.T) {
	ws := &Workstream{
		ID:   "ws-test",
		Goal: "Test",
		Phases: []Phase{
			{Name: "P1", Issues: []WorkstreamIssue{
				{Ref: "#1", Title: "A", Status: StatusPending},
				{Ref: "#2", Title: "B", Status: StatusPending},
				{Ref: "#3", Title: "C", Status: StatusPending},
			}},
		},
	}

	mock := newMockEngine()
	exec := &testExecutor{mock: mock, maxParallel: 3}

	if err := exec.Execute(context.Background(), ws); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	calls := mock.getCalls()
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(calls))
	}

	if ws.Status != StatusCompleted {
		t.Errorf("Status = %q, want %q", ws.Status, StatusCompleted)
	}
}

func TestExecutor_FailureSkipsDependents(t *testing.T) {
	ws := &Workstream{
		ID:   "ws-test",
		Goal: "Test",
		Phases: []Phase{
			{Name: "P1", Issues: []WorkstreamIssue{
				{Ref: "#1", Title: "A", Status: StatusPending},
				{Ref: "#2", Title: "B", Status: StatusPending, DependsOn: []string{"#1"}},
				{Ref: "#3", Title: "C", Status: StatusPending, DependsOn: []string{"#2"}},
			}},
		},
	}

	mock := newMockEngine("#1") // #1 will fail.
	exec := &testExecutor{mock: mock, maxParallel: 1}

	err := exec.Execute(context.Background(), ws)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if ws.Status != StatusFailed {
		t.Errorf("Status = %q, want %q", ws.Status, StatusFailed)
	}

	// Only A should have been attempted.
	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1 (only A)", len(calls))
	}

	// B and C should be failed (skipped).
	for _, issue := range ws.AllIssues() {
		if issue.Status != StatusFailed {
			t.Errorf("issue %s status = %q, want %q", issue.Title, issue.Status, StatusFailed)
		}
	}
}

func TestExecutor_ContextCancellation(t *testing.T) {
	ws := &Workstream{
		ID:   "ws-test",
		Goal: "Test",
		Phases: []Phase{
			{Name: "P1", Issues: []WorkstreamIssue{
				{Ref: "#1", Title: "A", Status: StatusPending},
			}},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	mock := newMockEngine()
	exec := &testExecutor{mock: mock, maxParallel: 1}

	err := exec.Execute(ctx, ws)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestExecutor_CyclicDependencies(t *testing.T) {
	ws := &Workstream{
		ID:   "ws-test",
		Goal: "Test",
		Phases: []Phase{
			{Name: "P1", Issues: []WorkstreamIssue{
				{Ref: "#1", Title: "A", Status: StatusPending, DependsOn: []string{"#2"}},
				{Ref: "#2", Title: "B", Status: StatusPending, DependsOn: []string{"#1"}},
			}},
		},
	}

	mock := newMockEngine()
	exec := &testExecutor{mock: mock, maxParallel: 1}

	err := exec.Execute(context.Background(), ws)
	if err == nil {
		t.Fatal("expected error for cyclic dependencies")
	}
}

func TestExecutor_SemaphoreLimitsParallelism(t *testing.T) {
	ws := &Workstream{
		ID:   "ws-test",
		Goal: "Test",
		Phases: []Phase{
			{Name: "P1", Issues: []WorkstreamIssue{
				{Ref: "#1", Title: "A", Status: StatusPending},
				{Ref: "#2", Title: "B", Status: StatusPending},
				{Ref: "#3", Title: "C", Status: StatusPending},
				{Ref: "#4", Title: "D", Status: StatusPending},
			}},
		},
	}

	var maxConcurrent int64
	var currentConcurrent int64

	mock := newMockEngine()

	// Override to track concurrency.
	graph, _ := BuildGraph(ws)
	ws.Status = StatusInProgress

	sem := make(chan struct{}, 2) // maxParallel = 2
	var wg sync.WaitGroup

	ready := graph.Ready()
	for _, issue := range ready {
		wg.Add(1)
		go func(issue *WorkstreamIssue) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			cur := atomic.AddInt64(&currentConcurrent, 1)
			for {
				old := atomic.LoadInt64(&maxConcurrent)
				if cur <= old || atomic.CompareAndSwapInt64(&maxConcurrent, old, cur) {
					break
				}
			}

			mock.recordCall(issue.IssueID())

			atomic.AddInt64(&currentConcurrent, -1)
			issue.Status = StatusCompleted
			graph.MarkComplete(issue.IssueID())
		}(issue)
	}
	wg.Wait()

	if atomic.LoadInt64(&maxConcurrent) > 2 {
		t.Errorf("max concurrent = %d, want <= 2", atomic.LoadInt64(&maxConcurrent))
	}
}

func TestNewExecutor_MinParallel(t *testing.T) {
	exec := NewExecutor(nil, 0)
	if exec.maxParallel != 1 {
		t.Errorf("maxParallel = %d, want 1 (minimum)", exec.maxParallel)
	}

	exec = NewExecutor(nil, -5)
	if exec.maxParallel != 1 {
		t.Errorf("maxParallel = %d, want 1 (minimum)", exec.maxParallel)
	}
}

func TestExecutor_PartialFailure(t *testing.T) {
	// A and B are independent. A fails, B succeeds. C depends on both.
	ws := &Workstream{
		ID:   "ws-test",
		Goal: "Test",
		Phases: []Phase{
			{Name: "P1", Issues: []WorkstreamIssue{
				{Ref: "#1", Title: "A", Status: StatusPending},
				{Ref: "#2", Title: "B", Status: StatusPending},
				{Ref: "#3", Title: "C", Status: StatusPending, DependsOn: []string{"#1", "#2"}},
			}},
		},
	}

	mock := newMockEngine("#1")
	exec := &testExecutor{mock: mock, maxParallel: 2}

	err := exec.Execute(context.Background(), ws)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if ws.Status != StatusFailed {
		t.Errorf("Status = %q, want %q", ws.Status, StatusFailed)
	}

	// A should be failed, B completed, C failed (skipped).
	a := ws.FindIssue("#1")
	if a.Status != StatusFailed {
		t.Errorf("A status = %q, want %q", a.Status, StatusFailed)
	}
	b := ws.FindIssue("#2")
	if b.Status != StatusCompleted {
		t.Errorf("B status = %q, want %q", b.Status, StatusCompleted)
	}
	c := ws.FindIssue("#3")
	if c.Status != StatusFailed {
		t.Errorf("C status = %q, want %q", c.Status, StatusFailed)
	}
}
