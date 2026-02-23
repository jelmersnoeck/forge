package planner

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/jelmersnoeck/forge/internal/engine"
)

// Executor processes workstream issues in dependency order, running up to
// maxParallel builds concurrently via the engine.
type Executor struct {
	engine      *engine.Engine
	maxParallel int
}

// NewExecutor creates a new workstream executor.
func NewExecutor(engine *engine.Engine, maxParallel int) *Executor {
	if maxParallel < 1 {
		maxParallel = 1
	}
	return &Executor{
		engine:      engine,
		maxParallel: maxParallel,
	}
}

// Execute processes all issues in a workstream respecting dependency order.
// It runs up to maxParallel builds concurrently using a semaphore pattern.
// If an issue fails, all transitive dependents are skipped.
func (e *Executor) Execute(ctx context.Context, ws *Workstream) error {
	slog.Info("starting workstream execution",
		"workstream_id", ws.ID,
		"max_parallel", e.maxParallel,
	)

	graph, err := BuildGraph(ws)
	if err != nil {
		return fmt.Errorf("building dependency graph: %w", err)
	}

	if graph.HasCycle() {
		return fmt.Errorf("workstream %s has cyclic dependencies", ws.ID)
	}

	ws.Status = StatusInProgress

	// Track failed issues and their transitive dependents.
	var failMu sync.Mutex
	skipped := make(map[string]bool)

	// Process in waves until all issues are complete.
	for !graph.IsComplete() {
		// Check context cancellation.
		if ctx.Err() != nil {
			ws.Status = StatusFailed
			return fmt.Errorf("workstream execution cancelled: %w", ctx.Err())
		}

		ready := graph.Ready()
		if len(ready) == 0 {
			// No ready issues and not complete means everything remaining
			// is blocked by failed dependencies.
			slog.Warn("no ready issues but workstream not complete; remaining issues blocked by failures")
			break
		}

		// Filter out skipped issues.
		var toRun []*WorkstreamIssue
		for _, issue := range ready {
			id := issue.IssueID()
			failMu.Lock()
			isSkipped := skipped[id]
			failMu.Unlock()
			if isSkipped {
				slog.Info("skipping issue due to failed dependency", "issue", id)
				issue.Status = StatusFailed
				graph.MarkFailed(id)
				continue
			}
			toRun = append(toRun, issue)
		}

		if len(toRun) == 0 {
			continue
		}

		slog.Info("executing wave", "ready_issues", len(toRun))

		// Execute ready issues in parallel with semaphore.
		sem := make(chan struct{}, e.maxParallel)
		var wg sync.WaitGroup

		for _, issue := range toRun {
			wg.Add(1)
			go func(issue *WorkstreamIssue) {
				defer wg.Done()

				sem <- struct{}{}        // Acquire semaphore.
				defer func() { <-sem }() // Release semaphore.

				id := issue.IssueID()
				issue.Status = StatusInProgress

				slog.Info("building issue", "issue", id, "title", issue.Title)

				buildErr := e.buildIssue(ctx, ws, issue)
				if buildErr != nil {
					slog.Error("issue build failed", "issue", id, "error", buildErr)
					issue.Status = StatusFailed
					graph.MarkFailed(id)

					// Mark all transitive dependents as skipped.
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
				slog.Info("issue completed", "issue", id)
			}(issue)
		}

		wg.Wait()
	}

	// Determine final workstream status.
	allCompleted := true
	failedCount := 0
	totalCount := 0
	for _, issue := range ws.AllIssues() {
		totalCount++
		if issue.Status != StatusCompleted {
			allCompleted = false
		}
		if issue.Status == StatusFailed {
			failedCount++
		}
	}

	if allCompleted {
		ws.Status = StatusCompleted
	} else if failedCount > 0 {
		ws.Status = StatusFailed
	}

	slog.Info("workstream execution finished",
		"workstream_id", ws.ID,
		"status", ws.Status,
	)

	if failedCount > 0 {
		return fmt.Errorf("workstream %s: %d of %d issues failed", ws.ID, failedCount, totalCount)
	}

	return nil
}

// buildIssue runs engine.Build for a single workstream issue.
func (e *Executor) buildIssue(ctx context.Context, ws *Workstream, issue *WorkstreamIssue) error {
	if issue.Ref == "" {
		return fmt.Errorf("issue %q has no tracker reference; create issues first", issue.Title)
	}

	req := engine.BuildRequest{
		IssueRef:   issue.Ref,
		BaseBranch: "main",
	}

	result, err := e.engine.Build(ctx, req)
	if err != nil {
		return fmt.Errorf("engine build: %w", err)
	}

	if result.Status != engine.BuildStatusSuccess {
		return fmt.Errorf("build finished with status %s: %s", result.Status, result.Error)
	}

	return nil
}
