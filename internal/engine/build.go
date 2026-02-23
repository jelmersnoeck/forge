package engine

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/gitutil"
	"github.com/jelmersnoeck/forge/internal/principles"
	"github.com/jelmersnoeck/forge/internal/review"
	"github.com/jelmersnoeck/forge/internal/tracker"
)

// Build executes the governed build loop:
//
//	Issue -> Plan -> [Approval] -> Code -> Review -> (critical? loop : PR)
//
// The loop runs up to MaxIterations times. Each iteration runs the code agent,
// then reviews the output. If the review finds critical issues, feedback is
// assembled and passed back to the code agent for the next iteration.
func (e *Engine) Build(ctx context.Context, req BuildRequest) (*BuildResult, error) {
	slog.Info("starting governed build", "issue_ref", req.IssueRef, "max_iterations", e.config.MaxIterations)

	result := &BuildResult{
		Status: BuildStatusFailed,
	}

	// Step 1: Resolve issue reference and fetch issue from tracker.
	ref, err := tracker.ParseIssueRef(req.IssueRef)
	if err != nil {
		result.Error = fmt.Sprintf("parsing issue reference: %v", err)
		return result, fmt.Errorf("parsing issue reference: %w", err)
	}

	trackerName := ref.Tracker
	if trackerName == "" {
		trackerName = e.config.DefaultTracker
	}
	t, ok := e.trackers[trackerName]
	if !ok {
		result.Error = fmt.Sprintf("tracker %q not configured", trackerName)
		return result, fmt.Errorf("tracker %q not configured", trackerName)
	}

	issue, err := t.GetIssue(ctx, req.IssueRef)
	if err != nil {
		result.Error = fmt.Sprintf("fetching issue: %v", err)
		return result, fmt.Errorf("fetching issue %s: %w", req.IssueRef, err)
	}
	result.Issue = issue

	// Step 2: Create git branch.
	git := gitutil.New(req.WorkDir)
	branchName := gitutil.FormatBranch(e.config.BranchPattern, map[string]string{
		"Tracker": ref.Tracker,
		"IssueID": ref.ID,
	})

	if err := git.CreateBranch(ctx, branchName); err != nil {
		result.Error = fmt.Sprintf("creating branch: %v", err)
		return result, fmt.Errorf("creating branch %s: %w", branchName, err)
	}

	slog.Info("created branch", "branch", branchName)

	// Step 3: Run plan agent.
	planResult, err := e.Plan(ctx, PlanRequest{
		IssueRef:      req.IssueRef,
		PrincipleSets: req.PrincipleSets,
		WorkDir:       req.WorkDir,
	})
	if err != nil {
		result.Error = fmt.Sprintf("planning: %v", err)
		return result, fmt.Errorf("planning: %w", err)
	}
	result.Plan = planResult.Plan

	// Step 4: If approval required, return for CLI to handle.
	if e.config.RequireApproval {
		slog.Info("plan requires approval, returning for review")
		result.Status = BuildStatusRejected
		return result, nil
	}

	// Step 5-8: Code -> Review loop.
	baseBranch := req.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	for iteration := 1; iteration <= e.config.MaxIterations; iteration++ {
		result.Iterations = iteration
		slog.Info("starting build iteration", "iteration", iteration, "max", e.config.MaxIterations)

		// Step 5: Run code agent.
		codeErr := e.runCodeAgent(ctx, req, issue, planResult.Plan, result.feedbackFromLastReview())
		if codeErr != nil {
			result.Error = fmt.Sprintf("code agent (iteration %d): %v", iteration, codeErr)
			return result, fmt.Errorf("code agent iteration %d: %w", iteration, codeErr)
		}

		// Stage all changes so new files appear in the diff.
		if err := git.AddAll(ctx); err != nil {
			result.Error = fmt.Sprintf("staging changes: %v", err)
			return result, fmt.Errorf("staging changes: %w", err)
		}

		// Commit changes so we can diff against the base branch.
		commitMsg := fmt.Sprintf("forge: iteration %d for %s", iteration, req.IssueRef)
		if err := git.Commit(ctx, commitMsg); err != nil {
			// Commit may fail if there are no changes; that's OK.
			slog.Warn("commit failed (may have no changes)", "error", err)
		}

		// Step 6: Run review.
		diff, err := git.Diff(ctx, baseBranch)
		if err != nil {
			result.Error = fmt.Sprintf("getting diff: %v", err)
			return result, fmt.Errorf("getting diff: %w", err)
		}

		if diff == "" && iteration == 1 {
			slog.Warn("empty diff after first code iteration; the code agent may not have made changes",
				"issue_ref", req.IssueRef,
			)
		}

		reviewResult, err := e.Review(ctx, ReviewRequest{
			Diff:          diff,
			PrincipleSets: req.PrincipleSets,
			WorkDir:       req.WorkDir,
		})
		if err != nil {
			result.Error = fmt.Sprintf("review (iteration %d): %v", iteration, err)
			return result, fmt.Errorf("review iteration %d: %w", iteration, err)
		}
		result.Reviews = append(result.Reviews, *reviewResult)

		// Step 7: Check for critical findings.
		if !reviewResult.HasCritical {
			slog.Info("review clean, proceeding to PR", "iteration", iteration)

			// Step 9: Create PR via tracker.
			pr, err := e.createPR(ctx, t, issue, branchName, baseBranch, ref)
			if err != nil {
				result.Error = fmt.Sprintf("creating PR: %v", err)
				return result, fmt.Errorf("creating PR: %w", err)
			}
			result.PR = pr
			result.Status = BuildStatusSuccess
			slog.Info("governed build complete", "pr_url", pr.URL, "iterations", iteration)
			return result, nil
		}

		// Step 8: Max iterations check.
		if iteration == e.config.MaxIterations {
			slog.Warn("max iterations reached with critical findings",
				"iterations", iteration,
				"critical_findings", countCritical(reviewResult.Findings),
			)
			result.Status = BuildStatusMaxLoops
			result.Error = fmt.Sprintf("max iterations (%d) reached with critical findings", e.config.MaxIterations)
			return result, nil
		}

		slog.Info("critical findings found, looping back to code agent",
			"iteration", iteration,
			"critical_findings", countCritical(reviewResult.Findings),
		)
	}

	return result, nil
}

// runCodeAgent executes the code agent with the full context of the issue,
// plan, and any feedback from prior review iterations.
func (e *Engine) runCodeAgent(ctx context.Context, req BuildRequest, issue *tracker.Issue, plan string, feedback string) error {
	coderAgent, err := e.getAgent(e.config.CoderAgent)
	if err != nil {
		return fmt.Errorf("getting coder agent: %w", err)
	}

	prompt := fmt.Sprintf(
		"You are a software engineer implementing a feature.\n\n"+
			"## Issue\n\n"+
			"**Title:** %s\n\n"+
			"**Description:**\n%s\n\n"+
			"## Plan\n\n%s\n",
		issue.Title,
		issue.Body,
		plan,
	)

	if feedback != "" {
		prompt += fmt.Sprintf("\n## Review Feedback\n\nThe following issues were found in your previous implementation. Fix them:\n\n%s\n", feedback)
	}

	resp, err := coderAgent.Run(ctx, agent.Request{
		Prompt:  prompt,
		WorkDir: req.WorkDir,
		Mode:    agent.ModeCode,
		Permissions: agent.ToolPermissions{
			Read:    true,
			Write:   true,
			Execute: true,
			Network: false,
		},
		OutputFormat: "text",
	})
	if err != nil {
		return fmt.Errorf("running code agent: %w", err)
	}

	if resp.Error != "" {
		return fmt.Errorf("code agent error: %s", resp.Error)
	}

	return nil
}

// createPR creates a pull request via the tracker.
func (e *Engine) createPR(ctx context.Context, t tracker.Tracker, issue *tracker.Issue, head, base string, ref *tracker.IssueRef) (*tracker.PullRequest, error) {
	pr, err := t.CreatePR(ctx, &tracker.CreatePRRequest{
		Title:    fmt.Sprintf("[Forge] %s", issue.Title),
		Body:     fmt.Sprintf("Automated implementation for %s\n\n%s", ref.String(), issue.Body),
		Head:     head,
		Base:     base,
		Repo:     issue.Repo,
		IssueRef: ref.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("creating pull request: %w", err)
	}
	return pr, nil
}

// feedbackFromLastReview extracts feedback text from the most recent review
// in a BuildResult, to be passed back to the code agent.
func (r *BuildResult) feedbackFromLastReview() string {
	if len(r.Reviews) == 0 {
		return ""
	}
	lastReview := r.Reviews[len(r.Reviews)-1]
	if len(lastReview.Findings) == 0 {
		return ""
	}

	feedback := ""
	for i, f := range lastReview.Findings {
		feedback += fmt.Sprintf("%d. [%s] %s", i+1, f.Severity, f.Message)
		if f.File != "" {
			feedback += fmt.Sprintf(" (file: %s", f.File)
			if f.Line > 0 {
				feedback += fmt.Sprintf(", line: %d", f.Line)
			}
			feedback += ")"
		}
		if f.Suggestion != "" {
			feedback += fmt.Sprintf("\n   Suggestion: %s", f.Suggestion)
		}
		feedback += "\n"
	}
	return feedback
}

// countCritical returns the number of critical findings in a list.
func countCritical(findings []review.Finding) int {
	count := 0
	for _, f := range findings {
		if f.Severity == principles.SeverityCritical {
			count++
		}
	}
	return count
}
