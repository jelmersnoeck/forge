package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jelmersnoeck/forge/internal/engine"
	"github.com/jelmersnoeck/forge/internal/gitutil"
	"github.com/jelmersnoeck/forge/internal/review"
	"github.com/spf13/cobra"
)

var (
	reviewDiff       string
	reviewPrinciples string
	reviewFormat     string
)

var reviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Review a diff against governance principles",
	Long: `Run a standalone review of a diff against loaded principles.

The review command accepts a git diff range (e.g., HEAD~1..HEAD) or a file
containing a unified diff, evaluates it against configured governance
principles, and outputs findings.

Exit code is 1 if critical findings are found (suitable for CI).

Example:
  forge review --diff HEAD~1..HEAD
  forge review --diff main...feature-branch --format sarif
  forge review --diff ./patch.diff --principles security,architecture`,
	RunE: runReview,
}

func init() {
	reviewCmd.Flags().StringVar(&reviewDiff, "diff", "", "Diff range (e.g. HEAD~1..HEAD) or file path (required)")
	reviewCmd.Flags().StringVar(&reviewPrinciples, "principles", "", "Comma-separated principle sets to apply")
	reviewCmd.Flags().StringVar(&reviewFormat, "format", "text", "Output format: text, json, or sarif")

	_ = reviewCmd.MarkFlagRequired("diff")

	rootCmd.AddCommand(reviewCmd)
}

func runReview(cmd *cobra.Command, args []string) error {
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
	if reviewPrinciples != "" {
		principleSets = splitAndTrim(reviewPrinciples)
	} else if len(cfg.Principles.Active) > 0 {
		principleSets = cfg.Principles.Active
	}

	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	// Resolve the diff: either a file path or a git diff range.
	diff, err := resolveDiff(workDir, reviewDiff)
	if err != nil {
		return fmt.Errorf("resolving diff: %w", err)
	}

	req := engine.ReviewRequest{
		Diff:          diff,
		PrincipleSets: principleSets,
		WorkDir:       workDir,
	}

	ctx := context.Background()
	result, err := eng.Review(ctx, req)
	if err != nil {
		return fmt.Errorf("review failed: %w", err)
	}

	if err := outputReviewResult(result, reviewFormat); err != nil {
		return err
	}

	// Exit code 1 if critical findings found (for CI use).
	if result.HasCritical {
		os.Exit(1)
	}

	return nil
}

// resolveDiff resolves a --diff flag value to a unified diff string.
// If the value is an existing file path, its contents are read.
// Otherwise, it is treated as a git diff range.
func resolveDiff(workDir, diffArg string) (string, error) {
	// Check if it is an existing file.
	if info, err := os.Stat(diffArg); err == nil && !info.IsDir() {
		data, err := os.ReadFile(diffArg)
		if err != nil {
			return "", fmt.Errorf("reading diff file %s: %w", diffArg, err)
		}
		return string(data), nil
	}

	// Treat as a git diff range.
	git := gitutil.New(workDir)
	diff, err := git.DiffRef(context.Background(), diffArg)
	if err != nil {
		return "", fmt.Errorf("running git diff %s: %w", diffArg, err)
	}

	return diff, nil
}

// outputReviewResult formats and prints the review result.
func outputReviewResult(result *review.Result, format string) error {
	switch format {
	case "json":
		return outputJSON(result)
	case "sarif":
		return outputSARIF(result)
	default:
		return outputReviewText(result)
	}
}

// outputReviewText prints a human-readable review result.
func outputReviewText(result *review.Result) error {
	if len(result.Findings) == 0 {
		fmt.Println("Review complete: no findings.")
		return nil
	}

	fmt.Printf("Review complete: %d finding(s)\n\n", len(result.Findings))

	// Print as a table-like format.
	for i, f := range result.Findings {
		fmt.Printf("%d. [%s] %s\n", i+1, f.Severity, f.Message)
		if f.File != "" {
			loc := f.File
			if f.Line > 0 {
				loc = fmt.Sprintf("%s:%d", f.File, f.Line)
			}
			fmt.Printf("   Location: %s\n", loc)
		}
		if f.PrincipleID != "" {
			fmt.Printf("   Principle: %s\n", f.PrincipleID)
		}
		if f.Suggestion != "" {
			fmt.Printf("   Suggestion: %s\n", f.Suggestion)
		}
		fmt.Println()
	}

	if result.HasCritical {
		fmt.Println("CRITICAL findings detected. Review must be addressed before merge.")
	}

	return nil
}

// outputSARIF outputs findings in SARIF v2.1.0 format.
func outputSARIF(result *review.Result) error {
	data, err := review.ToSARIF(result.Findings, "forge-review")
	if err != nil {
		return fmt.Errorf("generating SARIF output: %w", err)
	}
	fmt.Println(string(data))
	return nil
}
