package engine

import (
	"fmt"
	"log/slog"

	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/principles"
	"github.com/jelmersnoeck/forge/internal/tracker"
)

// New creates a new Engine with the given dependencies.
// It validates that the configured agent and tracker backend names
// exist in the provided maps.
func New(cfg *EngineConfig, agents map[string]agent.Agent, trackers map[string]tracker.Tracker, store *principles.Store) (*Engine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("creating engine: config is required")
	}

	// Apply defaults.
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 3
	}
	if cfg.BranchPattern == "" {
		cfg.BranchPattern = "forge/{{.Tracker}}-{{.IssueID}}"
	}
	if cfg.SeverityThreshold == "" {
		cfg.SeverityThreshold = "critical"
	}

	// Resolve agent role names to defaults if not set.
	if cfg.PlannerAgent == "" {
		cfg.PlannerAgent = cfg.DefaultAgent
	}
	if cfg.CoderAgent == "" {
		cfg.CoderAgent = cfg.DefaultAgent
	}
	if cfg.ReviewerAgent == "" {
		cfg.ReviewerAgent = cfg.DefaultAgent
	}

	// Validate that configured agents exist.
	for _, name := range []string{cfg.PlannerAgent, cfg.CoderAgent, cfg.ReviewerAgent} {
		if name == "" {
			continue
		}
		if _, ok := agents[name]; !ok {
			return nil, fmt.Errorf("creating engine: agent backend %q not found", name)
		}
	}

	// Validate that the default tracker exists.
	if cfg.DefaultTracker != "" {
		if _, ok := trackers[cfg.DefaultTracker]; !ok {
			return nil, fmt.Errorf("creating engine: tracker backend %q not found", cfg.DefaultTracker)
		}
	}

	slog.Info("engine created",
		"max_iterations", cfg.MaxIterations,
		"branch_pattern", cfg.BranchPattern,
		"planner_agent", cfg.PlannerAgent,
		"coder_agent", cfg.CoderAgent,
		"reviewer_agent", cfg.ReviewerAgent,
		"default_tracker", cfg.DefaultTracker,
	)

	return &Engine{
		Agents:     agents,
		Trackers:   trackers,
		Principles: store,
		Config:     cfg,
	}, nil
}
