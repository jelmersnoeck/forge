package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/jelmersnoeck/forge/internal/engine"
	"github.com/spf13/cobra"
)

var (
	buildIssue      string
	buildPrinciples string
	buildBranch     string
	buildFormat     string
	buildWorkstream string
)

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Run the governed build loop for an issue",
	Long: `Execute the governed build loop: Issue -> Plan -> [Approval] -> Code -> Review -> PR.

The build command fetches an issue from the configured tracker, generates an
implementation plan, runs the code agent, reviews the output against loaded
principles, and creates a pull request if the review passes.

Use --workstream to execute all issues in a workstream YAML in dependency order.

Example:
  forge build --issue gh:org/repo#123
  forge build --issue #42 --branch develop --format json
  forge build --workstream .forge/workstreams/ws-auth.yaml`,
	RunE: runBuild,
}

func init() {
	buildCmd.Flags().StringVar(&buildIssue, "issue", "", "Issue reference (e.g. gh:org/repo#123, #42)")
	buildCmd.Flags().StringVar(&buildPrinciples, "principles", "", "Comma-separated principle sets to apply (default: from config)")
	buildCmd.Flags().StringVar(&buildBranch, "branch", "main", "Base branch to create PR against")
	buildCmd.Flags().StringVar(&buildFormat, "format", "text", "Output format: text or json")
	buildCmd.Flags().StringVar(&buildWorkstream, "workstream", "", "Path to workstream YAML for multi-issue execution")

	rootCmd.AddCommand(buildCmd)
}

func runBuild(cmd *cobra.Command, args []string) error {
	// Workstream mode: execute all issues in dependency order.
	if buildWorkstream != "" {
		return runWorkstreamBuild(buildWorkstream)
	}

	// Single issue mode: requires --issue.
	if buildIssue == "" {
		return fmt.Errorf("either --issue or --workstream is required")
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
	if buildPrinciples != "" {
		principleSets = splitAndTrim(buildPrinciples)
	} else if len(cfg.Principles.Active) > 0 {
		principleSets = cfg.Principles.Active
	}

	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	req := engine.BuildRequest{
		IssueRef:      buildIssue,
		PrincipleSets: principleSets,
		WorkDir:       workDir,
		BaseBranch:    buildBranch,
	}

	ctx := context.Background()
	result, err := eng.Build(ctx, req)
	if err != nil {
		return formatBuildError(err, buildFormat)
	}

	return outputBuildResult(result, buildFormat)
}

// outputBuildResult formats and prints the build result.
func outputBuildResult(result *engine.BuildResult, format string) error {
	if format == "json" {
		return outputJSON(result)
	}
	return outputBuildText(result)
}

// outputBuildText prints a human-readable build result.
func outputBuildText(result *engine.BuildResult) error {
	fmt.Printf("Build Status: %s\n", result.Status)

	if result.Issue != nil {
		fmt.Printf("Issue: %s\n", result.Issue.Title)
	}

	if result.Plan != "" {
		fmt.Println("\n--- Plan ---")
		fmt.Println(result.Plan)
	}

	if result.Iterations > 0 {
		fmt.Printf("\nIterations: %d\n", result.Iterations)
	}

	for i, rev := range result.Reviews {
		fmt.Printf("\n--- Review (iteration %d) ---\n", i+1)
		fmt.Printf("Findings: %d\n", len(rev.Findings))
		fmt.Printf("Has Critical: %v\n", rev.HasCritical)
		for _, f := range rev.Findings {
			fmt.Printf("  [%s] %s", f.Severity, f.Message)
			if f.File != "" {
				fmt.Printf(" (%s", f.File)
				if f.Line > 0 {
					fmt.Printf(":%d", f.Line)
				}
				fmt.Print(")")
			}
			fmt.Println()
		}
	}

	if result.PR != nil {
		fmt.Printf("\nPull Request: %s\n", result.PR.URL)
	}

	if result.Error != "" {
		fmt.Fprintf(os.Stderr, "\nError: %s\n", result.Error)
	}

	return nil
}

// formatBuildError formats an error for the given output format.
func formatBuildError(err error, format string) error {
	if format == "json" {
		errResult := map[string]string{
			"status": "error",
			"error":  err.Error(),
		}
		data, _ := json.MarshalIndent(errResult, "", "  ")
		fmt.Fprintln(os.Stderr, string(data))
	}
	return err
}

// splitAndTrim splits a comma-separated string and trims whitespace.
func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// outputJSON marshals v as indented JSON and prints to stdout.
func outputJSON(v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling JSON: %w", err)
	}
	fmt.Println(string(data))
	return nil
}
