package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jelmersnoeck/forge/internal/planner"
	"github.com/jelmersnoeck/forge/pkg/config"
	"github.com/spf13/cobra"
)

// runWorkstreamBuild handles `forge build --workstream <path>`.
// It loads the workstream YAML, creates an executor, and runs all issues
// in dependency order.
func runWorkstreamBuild(workstreamPath string) error {
	ws, err := planner.LoadWorkstream(workstreamPath)
	if err != nil {
		return fmt.Errorf("loading workstream: %w", err)
	}

	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	eng, err := buildEngine(cfg)
	if err != nil {
		return fmt.Errorf("building engine: %w", err)
	}

	maxParallel := 2 // Default parallelism.
	executor := planner.NewExecutor(eng, maxParallel)

	ctx := context.Background()
	if err := executor.Execute(ctx, ws); err != nil {
		return fmt.Errorf("workstream execution: %w", err)
	}

	// Save updated workstream state.
	if err := planner.SaveWorkstream(ws, workstreamPath); err != nil {
		return fmt.Errorf("saving workstream state: %w", err)
	}

	fmt.Printf("Workstream %s completed with status: %s\n", ws.ID, ws.Status)
	return nil
}

// addPlanGoalFlags adds the --goal and --context flags to the plan command
// for workstream planning mode. Called from plan.go init().
func addPlanGoalFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&planGoal, "goal", "", "High-level goal for workstream planning")
	cmd.Flags().StringSliceVar(&planContext, "context", nil, "Context files to include in planning")
	cmd.Flags().BoolVar(&planExecute, "execute", false, "Create issues in tracker from workstream plan")
	cmd.Flags().StringVar(&planWorkstream, "workstream", "", "Path to workstream YAML (for --execute)")
}

var (
	planGoal       string
	planContext    []string
	planExecute    bool
	planWorkstream string
)

// runPlanGoal handles `forge plan --goal "..."`.
func runPlanGoal(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	eng, err := buildEngine(cfg)
	if err != nil {
		return fmt.Errorf("building engine: %w", err)
	}

	// Get the planner agent.
	plannerAgentName := cfg.Agent.Roles.Planner
	if plannerAgentName == "" {
		plannerAgentName = cfg.Agent.Default
	}

	agents, err := buildAgents(cfg)
	if err != nil {
		return fmt.Errorf("building agents: %w", err)
	}

	plannerAgent, ok := agents[plannerAgentName]
	if !ok {
		return fmt.Errorf("planner agent %q not configured", plannerAgentName)
	}

	// Get the default tracker.
	trackers, err := buildTrackers(cfg)
	if err != nil {
		return fmt.Errorf("building trackers: %w", err)
	}

	trackerName := cfg.Tracker.Default
	t, ok := trackers[trackerName]
	if !ok {
		return fmt.Errorf("tracker %q not configured", trackerName)
	}

	p := planner.New(plannerAgent, t)

	ctx := context.Background()
	ws, err := p.Plan(ctx, planner.PlanRequest{
		Goal:         planGoal,
		Tracker:      trackerName,
		Repo:         getDefaultRepo(cfg),
		ContextFiles: planContext,
	})
	if err != nil {
		return fmt.Errorf("planning: %w", err)
	}

	// Save workstream YAML.
	wsPath := fmt.Sprintf(".forge/workstreams/%s.yaml", ws.ID)
	if err := os.MkdirAll(".forge/workstreams", 0755); err != nil {
		return fmt.Errorf("creating workstream directory: %w", err)
	}
	if err := planner.SaveWorkstream(ws, wsPath); err != nil {
		return fmt.Errorf("saving workstream: %w", err)
	}

	fmt.Printf("Workstream saved to %s\n\n", wsPath)
	outputWorkstreamSummary(ws)

	// Suppress unused variable.
	_ = eng

	return nil
}

// runPlanExecute handles `forge plan --execute --workstream <path>`.
func runPlanExecute(cmd *cobra.Command, args []string) error {
	if planWorkstream == "" {
		return fmt.Errorf("--workstream flag is required with --execute")
	}

	ws, err := planner.LoadWorkstream(planWorkstream)
	if err != nil {
		return fmt.Errorf("loading workstream: %w", err)
	}

	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Get the planner agent.
	plannerAgentName := cfg.Agent.Roles.Planner
	if plannerAgentName == "" {
		plannerAgentName = cfg.Agent.Default
	}

	agents, err := buildAgents(cfg)
	if err != nil {
		return fmt.Errorf("building agents: %w", err)
	}

	plannerAgent, ok := agents[plannerAgentName]
	if !ok {
		return fmt.Errorf("planner agent %q not configured", plannerAgentName)
	}

	trackers, err := buildTrackers(cfg)
	if err != nil {
		return fmt.Errorf("building trackers: %w", err)
	}

	trackerName := cfg.Tracker.Default
	t, ok := trackers[trackerName]
	if !ok {
		return fmt.Errorf("tracker %q not configured", trackerName)
	}

	p := planner.New(plannerAgent, t)

	ctx := context.Background()
	if err := p.CreateIssues(ctx, ws); err != nil {
		return fmt.Errorf("creating issues: %w", err)
	}

	// Save updated workstream with refs.
	if err := planner.SaveWorkstream(ws, planWorkstream); err != nil {
		return fmt.Errorf("saving workstream: %w", err)
	}

	fmt.Printf("Issues created for workstream %s\n", ws.ID)
	fmt.Printf("Workstream updated at %s\n", planWorkstream)
	return nil
}

// outputWorkstreamSummary prints a human-readable summary of a workstream.
func outputWorkstreamSummary(ws *planner.Workstream) {
	fmt.Printf("Workstream: %s\n", ws.ID)
	fmt.Printf("Goal: %s\n", ws.Goal)
	fmt.Printf("Phases: %d\n", len(ws.Phases))
	fmt.Printf("Total Issues: %d\n\n", len(ws.AllIssues()))

	for _, phase := range ws.Phases {
		fmt.Printf("  Phase: %s (%d issues)\n", phase.Name, len(phase.Issues))
		for _, issue := range phase.Issues {
			deps := ""
			if len(issue.DependsOn) > 0 {
				deps = fmt.Sprintf(" [depends on: %s]", fmt.Sprint(issue.DependsOn))
			}
			fmt.Printf("    - %s%s\n", issue.Title, deps)
		}
	}
}

// getDefaultRepo returns the default repo from config.
func getDefaultRepo(cfg *config.Config) string {
	if cfg.Tracker.GitHub != nil {
		if cfg.Tracker.GitHub.Org != "" && cfg.Tracker.GitHub.DefaultRepo != "" {
			return cfg.Tracker.GitHub.Org + "/" + cfg.Tracker.GitHub.DefaultRepo
		}
	}
	return ""
}
