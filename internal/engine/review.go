package engine

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/principles"
	"github.com/jelmersnoeck/forge/internal/review"
)

// Review performs a standalone review of a diff against loaded principles.
// This is used by `forge review --diff` and also called internally during
// the build loop after each code iteration.
//
// Agent invocations are wrapped with retry logic for transient error recovery.
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
	if e.principles != nil && len(req.PrincipleSets) > 0 {
		var err error
		ps, err = e.principles.Load(ctx, req.PrincipleSets...)
		if err != nil {
			return nil, fmt.Errorf("loading principles: %w", err)
		}
	} else if e.principles != nil {
		// If no specific sets requested, use all loaded principles.
		ps = e.principles.All()
	}

	// Assemble the review prompt.
	prompt := principles.AssembleReviewPrompt(req.Diff, ps)

	// Get the reviewer agent.
	reviewerAgent, err := e.getAgent(e.config.ReviewerAgent)
	if err != nil {
		return nil, fmt.Errorf("getting reviewer agent: %w", err)
	}

	agentReq := agent.Request{
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
	}

	// Run the review agent with retry for transient failures.
	resp, err := RetryWithResult(ctx, e.config.Retry, func(ctx context.Context) (*agent.Response, error) {
		resp, err := reviewerAgent.Run(ctx, agentReq)
		if err != nil {
			return nil, fmt.Errorf("running review agent: %w", err)
		}
		if resp.Error != "" {
			return nil, fmt.Errorf("review agent error: %s", resp.Error)
		}
		return resp, nil
	})
	if err != nil {
		return nil, err
	}

	// Parse findings from agent output.
	findings, err := review.ParseFindings(resp.Output)
	if err != nil {
		return nil, fmt.Errorf("parsing review findings: %w", err)
	}

	// Apply severity threshold filtering.
	threshold := parseSeverity(e.config.SeverityThreshold)
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
