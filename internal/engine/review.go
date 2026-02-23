package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/principles"
	"github.com/jelmersnoeck/forge/internal/review"
)

// Review performs a standalone review of a diff against loaded principles.
// This is used by `forge review --diff` and also called internally during
// the build loop after each code iteration.
func (e *Engine) Review(ctx context.Context, req ReviewRequest) (*review.Result, error) {
	slog.Info("starting review", "principle_sets", req.PrincipleSets)

	if req.Diff == "" {
		return &review.Result{
			Findings:    nil,
			HasCritical: false,
		}, nil
	}

	// Load principles for the review.
	var ps []principles.Principle
	if e.Principles != nil && len(req.PrincipleSets) > 0 {
		var err error
		ps, err = e.Principles.Load(ctx, req.PrincipleSets...)
		if err != nil {
			return nil, fmt.Errorf("loading principles: %w", err)
		}
	} else if e.Principles != nil {
		// If no specific sets requested, use all loaded principles.
		ps = e.Principles.All()
	}

	// Assemble the review prompt.
	prompt := principles.AssembleReviewPrompt(req.Diff, ps)

	// Get the reviewer agent.
	reviewerAgent, err := e.getAgent(e.Config.ReviewerAgent)
	if err != nil {
		return nil, fmt.Errorf("getting reviewer agent: %w", err)
	}

	// Run the review agent with read-only permissions.
	resp, err := reviewerAgent.Run(ctx, agent.Request{
		Prompt:  prompt,
		WorkDir: req.WorkDir,
		Mode:    agent.ModeReview,
		Permissions: agent.ToolPermissions{
			Read:    true,
			Write:   false,
			Execute: false,
			Network: false,
		},
		OutputFormat: "json",
	})
	if err != nil {
		return nil, fmt.Errorf("running review agent: %w", err)
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("review agent error: %s", resp.Error)
	}

	// Parse findings from agent output.
	findings, err := parseFindings(resp.Output)
	if err != nil {
		slog.Warn("failed to parse review findings, treating as no findings",
			"error", err,
			"output_length", len(resp.Output),
		)
		// If we cannot parse, return empty result rather than failing.
		return &review.Result{
			Findings:    nil,
			HasCritical: false,
		}, nil
	}

	// Apply severity threshold filtering.
	threshold := parseSeverity(e.Config.SeverityThreshold)
	filtered := review.FilterBySeverity(findings, threshold)

	// Deduplicate findings.
	deduped := review.Deduplicate(filtered)

	result := &review.Result{
		Findings:          deduped,
		HasCritical:       review.HasCriticalFindings(deduped),
		PrinciplesCovered: review.CoveredPrinciples(deduped),
	}

	slog.Info("review complete",
		"total_findings", len(deduped),
		"has_critical", result.HasCritical,
		"principles_covered", len(result.PrinciplesCovered),
	)

	return result, nil
}

// parseFindings extracts review findings from agent JSON output.
// The agent output may contain a JSON array of findings or a JSON
// object with a "findings" key.
func parseFindings(output string) ([]review.Finding, error) {
	if output == "" {
		return nil, nil
	}

	// Try parsing as a direct array of findings.
	var findings []review.Finding
	if err := json.Unmarshal([]byte(output), &findings); err == nil {
		return findings, nil
	}

	// Try parsing as an object with a "findings" key.
	var wrapper struct {
		Findings []review.Finding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(output), &wrapper); err == nil {
		return wrapper.Findings, nil
	}

	return nil, fmt.Errorf("could not parse review output as findings JSON")
}

// parseSeverity converts a severity threshold string to a Severity value.
func parseSeverity(s string) principles.Severity {
	switch s {
	case "info":
		return principles.SeverityInfo
	case "warning":
		return principles.SeverityWarning
	case "critical":
		return principles.SeverityCritical
	default:
		return principles.SeverityCritical
	}
}
