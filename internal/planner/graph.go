package planner

import (
	"fmt"
	"sync"
)

// DependencyGraph manages the dependency relationships between workstream
// issues and tracks completion state for execution ordering.
type DependencyGraph struct {
	mu    sync.RWMutex
	nodes map[string]*graphNode
	edges map[string][]string // node ID -> list of dependents (children)
}

// graphNode wraps a WorkstreamIssue with completion tracking.
type graphNode struct {
	issue    *WorkstreamIssue
	deps     []string // IDs of issues this node depends on
	complete bool
}

// BuildGraph constructs a dependency graph from a workstream.
// It validates that all dependency references resolve to existing issues.
func BuildGraph(ws *Workstream) (*DependencyGraph, error) {
	g := &DependencyGraph{
		nodes: make(map[string]*graphNode),
		edges: make(map[string][]string),
	}

	allIssues := ws.AllIssues()

	// Register all nodes.
	for _, issue := range allIssues {
		id := issue.IssueID()
		if _, exists := g.nodes[id]; exists {
			return nil, fmt.Errorf("duplicate issue ID: %s", id)
		}
		g.nodes[id] = &graphNode{
			issue:    issue,
			deps:     issue.DependsOn,
			complete: issue.Status == StatusCompleted,
		}
	}

	// Build edges and validate dependencies.
	for _, issue := range allIssues {
		id := issue.IssueID()
		for _, dep := range issue.DependsOn {
			if _, exists := g.nodes[dep]; !exists {
				return nil, fmt.Errorf("issue %q depends on unknown issue %q", id, dep)
			}
			// dep -> id means "dep must complete before id can start"
			g.edges[dep] = append(g.edges[dep], id)
		}
	}

	return g, nil
}

// Ready returns all issues whose dependencies are satisfied (all deps complete)
// and that are not yet complete themselves.
func (g *DependencyGraph) Ready() []*WorkstreamIssue {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var ready []*WorkstreamIssue
	for _, node := range g.nodes {
		if node.complete {
			continue
		}
		if g.depsComplete(node) {
			ready = append(ready, node.issue)
		}
	}
	return ready
}

// depsComplete checks if all dependencies of a node are complete.
// Must be called with at least a read lock held.
func (g *DependencyGraph) depsComplete(node *graphNode) bool {
	for _, dep := range node.deps {
		depNode, exists := g.nodes[dep]
		if !exists || !depNode.complete {
			return false
		}
	}
	return true
}

// MarkComplete marks an issue as completed in the graph.
func (g *DependencyGraph) MarkComplete(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if node, exists := g.nodes[id]; exists {
		node.complete = true
	}
}

// MarkFailed marks an issue as failed in the graph. Failed issues are
// treated as complete (they will not be retried), but their dependents
// will be identified via TransitiveDependents.
func (g *DependencyGraph) MarkFailed(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if node, exists := g.nodes[id]; exists {
		node.complete = true
		node.issue.Status = StatusFailed
	}
}

// IsComplete returns true when all nodes in the graph are complete.
func (g *DependencyGraph) IsComplete() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	for _, node := range g.nodes {
		if !node.complete {
			return false
		}
	}
	return true
}

// HasCycle detects cycles in the dependency graph using Kahn's algorithm.
// Returns true if a cycle exists.
func (g *DependencyGraph) HasCycle() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// Compute in-degree for each node.
	inDegree := make(map[string]int)
	for id := range g.nodes {
		inDegree[id] = 0
	}
	for _, node := range g.nodes {
		id := node.issue.IssueID()
		inDegree[id] += len(node.deps)
	}

	// Seed queue with zero in-degree nodes.
	var queue []string
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	visited := 0
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		visited++

		// Reduce in-degree for all dependents.
		for _, dependent := range g.edges[current] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}

	return visited != len(g.nodes)
}

// TopologicalSort returns issues in a valid execution order (dependencies
// before dependents) using Kahn's algorithm. Returns an error if the graph
// contains a cycle.
func (g *DependencyGraph) TopologicalSort() ([]*WorkstreamIssue, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// Compute in-degree for each node.
	inDegree := make(map[string]int)
	for id := range g.nodes {
		inDegree[id] = 0
	}
	for _, node := range g.nodes {
		id := node.issue.IssueID()
		inDegree[id] += len(node.deps)
	}

	// Seed queue with zero in-degree nodes.
	var queue []string
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	var sorted []*WorkstreamIssue
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		sorted = append(sorted, g.nodes[current].issue)

		// Reduce in-degree for all dependents.
		for _, dependent := range g.edges[current] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}

	if len(sorted) != len(g.nodes) {
		return nil, fmt.Errorf("dependency graph contains a cycle")
	}

	return sorted, nil
}

// Dependents returns the IDs of issues that directly depend on the given issue.
func (g *DependencyGraph) Dependents(id string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	return g.edges[id]
}

// TransitiveDependents returns all issues that transitively depend on the
// given issue (direct and indirect dependents).
func (g *DependencyGraph) TransitiveDependents(id string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	visited := make(map[string]bool)
	var result []string

	var walk func(nodeID string)
	walk = func(nodeID string) {
		for _, dep := range g.edges[nodeID] {
			if !visited[dep] {
				visited[dep] = true
				result = append(result, dep)
				walk(dep)
			}
		}
	}
	walk(id)
	return result
}
