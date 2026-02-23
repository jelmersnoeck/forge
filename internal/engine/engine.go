// Package engine orchestrates the governed build loop — the core of Forge.
//
// The engine coordinates agents, trackers, principles, and environments
// to implement the Issue → Plan → Code → Review → PR workflow.
package engine

import (
	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/principles"
	"github.com/jelmersnoeck/forge/internal/review"
	"github.com/jelmersnoeck/forge/internal/tracker"
)

// Engine is the core orchestrator for governed builds.
type Engine struct {
	agents     map[string]agent.Agent
	trackers   map[string]tracker.Tracker
	principles *principles.Store
	config     *EngineConfig
}

// Config returns the engine's configuration.
func (e *Engine) Config() *EngineConfig {
	return e.config
}

// HasAgent reports whether an agent with the given name is registered.
func (e *Engine) HasAgent(name string) bool {
	_, ok := e.agents[name]
	return ok
}

// HasTracker reports whether a tracker with the given name is registered.
func (e *Engine) HasTracker(name string) bool {
	_, ok := e.trackers[name]
	return ok
}

// EngineConfig holds runtime configuration for the engine.
type EngineConfig struct {
	DefaultAgent     string // Default agent backend name.
	DefaultTracker   string // Default tracker backend name.
	MaxIterations    int    // Max review-fix loops (default 3).
	BranchPattern    string // Git branch naming pattern.
	TestCommand      string // Command to run tests.
	RequireApproval  bool   // Require human plan approval.
	SeverityThreshold string // Min severity to block PR.

	// Agent role mappings.
	PlannerAgent  string // Agent backend for planning.
	CoderAgent    string // Agent backend for coding.
	ReviewerAgent string // Agent backend for reviewing.
}

// BuildRequest contains everything needed to start a governed build.
type BuildRequest struct {
	IssueRef      string   // Issue reference (e.g., "gh:org/repo#123").
	PrincipleSets []string // Which principle sets to apply.
	WorkDir       string   // Repository working directory.
	BaseBranch    string   // Base branch to create PR against.
}

// BuildResult contains the outcome of a governed build.
type BuildResult struct {
	Issue       *tracker.Issue       // The issue that was built.
	Plan        string               // The generated plan.
	PR          *tracker.PullRequest // The created PR (if successful).
	Reviews     []review.Result      // All review iterations.
	Iterations  int                  // Number of review-fix cycles.
	Status      BuildStatus          // Final status.
	Error       string               // Error message if failed.
}

// BuildStatus indicates the final outcome of a build.
type BuildStatus string

const (
	BuildStatusSuccess   BuildStatus = "success"   // Clean review, PR created.
	BuildStatusFailed    BuildStatus = "failed"     // Build failed.
	BuildStatusMaxLoops  BuildStatus = "max_loops"  // Hit max iterations.
	BuildStatusRejected  BuildStatus = "rejected"   // Plan rejected by human.
)

// ReviewRequest contains everything needed for a standalone review.
type ReviewRequest struct {
	Diff          string   // Unified diff to review.
	PrincipleSets []string // Which principle sets to apply.
	WorkDir       string   // Repository working directory.
}

// PlanRequest contains everything needed to generate a plan.
type PlanRequest struct {
	IssueRef      string   // Issue reference.
	PrincipleSets []string // Which principle sets to apply.
	WorkDir       string   // Repository working directory.
}

// PlanResult contains the generated plan.
type PlanResult struct {
	Plan     string // The generated plan text.
	Approved bool   // Whether the plan was approved.
}
