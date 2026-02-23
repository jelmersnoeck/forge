package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jelmersnoeck/forge/internal/engine"
	"github.com/spf13/cobra"
)

var (
	planIssue      string
	planPrinciples string
	planFormat     string
)

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Generate an implementation plan for an issue or decompose a goal",
	Long: `Generate an implementation plan for an issue without executing it,
or decompose a high-level goal into a workstream with phases and issues.

Issue planning mode (default):
  forge plan --issue gh:org/repo#123
  forge plan --issue #42 --format json

Goal decomposition mode:
  forge plan --goal "Implement authentication system"
  forge plan --goal "Add API endpoints" --context ./specs/api.md

Issue creation from workstream:
  forge plan --execute --workstream .forge/workstreams/ws-auth.yaml`,
	RunE: runPlan,
}

func init() {
	planCmd.Flags().StringVar(&planIssue, "issue", "", "Issue reference (e.g. gh:org/repo#123, #42)")
	planCmd.Flags().StringVar(&planPrinciples, "principles", "", "Comma-separated principle sets to apply")
	planCmd.Flags().StringVar(&planFormat, "format", "text", "Output format: text or json")

	// Workstream planning flags (defined in workstream.go).
	addPlanGoalFlags(planCmd)

	rootCmd.AddCommand(planCmd)
}

func runPlan(cmd *cobra.Command, args []string) error {
	// Goal decomposition mode.
	if planGoal != "" {
		return runPlanGoal(cmd, args)
	}

	// Issue creation from workstream mode.
	if planExecute {
		return runPlanExecute(cmd, args)
	}

	// Single issue plan mode (original behavior).
	if planIssue == "" {
		return fmt.Errorf("one of --issue, --goal, or --execute is required")
	}

	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	eng, err := buildEngine(cfg)
	if err != nil {
		return fmt.Errorf("building engine: %w", err)
	}

	// Determine principle sets.
	var principleSets []string
	if planPrinciples != "" {
		principleSets = splitAndTrim(planPrinciples)
	} else if len(cfg.Principles.Active) > 0 {
		principleSets = cfg.Principles.Active
	}

	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	req := engine.PlanRequest{
		IssueRef:      planIssue,
		PrincipleSets: principleSets,
		WorkDir:       workDir,
	}

	ctx := context.Background()
	result, err := eng.Plan(ctx, req)
	if err != nil {
		return fmt.Errorf("plan failed: %w", err)
	}

	return outputPlanResult(result, planFormat)
}

// outputPlanResult formats and prints the plan result.
func outputPlanResult(result *engine.PlanResult, format string) error {
	if format == "json" {
		return outputJSON(result)
	}
	return outputPlanText(result)
}

// outputPlanText prints a human-readable plan.
func outputPlanText(result *engine.PlanResult) error {
	if result.Plan == "" {
		fmt.Println("No plan generated.")
		return nil
	}

	fmt.Println("--- Implementation Plan ---")
	fmt.Println()
	fmt.Println(result.Plan)
	fmt.Println()

	if result.Approved {
		fmt.Println("Status: Approved")
	} else {
		fmt.Println("Status: Pending approval")
		fmt.Println("Use 'forge build --issue <ref>' to execute this plan after review.")
	}

	return nil
}
