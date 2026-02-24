package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jelmersnoeck/forge/internal/agent"
)

// FeedbackRequest contains the context for processing PR review feedback.
type FeedbackRequest struct {
	PRNumber      int             `json:"pr_number"`
	RepoFullName  string          `json:"repo_full_name"`
	ReviewBody    string          `json:"review_body"`
	Comments      []ReviewComment `json:"comments"`
	WorkDir       string          `json:"work_dir,omitempty"`
	PrincipleSets []string        `json:"principle_sets,omitempty"`
}

// ReviewComment represents a PR review comment with file/line context.
type ReviewComment struct {
	Path     string `json:"path"`
	Line     int    `json:"line,omitempty"`
	Side     string `json:"side,omitempty"`
	Body     string `json:"body"`
	DiffHunk string `json:"diff_hunk,omitempty"`
}

// FeedbackResult contains the result of processing feedback.
type FeedbackResult struct {
	Status       string   `json:"status"`        // "applied", "partial", "failed"
	FilesChanged []string `json:"files_changed"`
	Summary      string   `json:"summary"`
}

// Feedback processes PR review feedback by sending comments to the coder agent.
// It builds a prompt from the review comments and runs the coder agent to
// implement the requested changes.
func (e *Engine) Feedback(ctx context.Context, req FeedbackRequest) (*FeedbackResult, error) {
	slog.Info("starting feedback processing",
		"pr_number", req.PRNumber,
		"repo", req.RepoFullName,
		"comments", len(req.Comments),
	)

	// Build the feedback prompt.
	prompt := buildFeedbackPrompt(req)

	// Get the coder agent.
	coderAgent, err := e.getAgent(e.config.CoderAgent)
	if err != nil {
		return nil, fmt.Errorf("getting coder agent: %w", err)
	}

	// Run the coder agent with full write permissions.
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
		return nil, fmt.Errorf("running feedback agent: %w", err)
	}

	if resp.Error != "" {
		return &FeedbackResult{
			Status:  "failed",
			Summary: fmt.Sprintf("agent error: %s", resp.Error),
		}, nil
	}

	result := &FeedbackResult{
		Status:       "applied",
		FilesChanged: resp.Files,
		Summary:      resp.Output,
	}

	slog.Info("feedback processing complete",
		"pr_number", req.PRNumber,
		"status", result.Status,
		"files_changed", len(result.FilesChanged),
	)

	return result, nil
}

// buildFeedbackPrompt constructs the prompt for the coder agent from
// the PR review comments.
func buildFeedbackPrompt(req FeedbackRequest) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf(
		"Human reviewer has requested changes on PR #%d in repo %s.\n\n",
		req.PRNumber, req.RepoFullName,
	))

	if req.ReviewBody != "" {
		b.WriteString("## Review Summary\n\n")
		b.WriteString(req.ReviewBody)
		b.WriteString("\n\n")
	}

	if len(req.Comments) > 0 {
		b.WriteString("## Inline Comments\n\n")
		for i, c := range req.Comments {
			b.WriteString(fmt.Sprintf("### Comment %d\n\n", i+1))
			if c.Path != "" {
				b.WriteString(fmt.Sprintf("**File:** `%s`", c.Path))
				if c.Line > 0 {
					b.WriteString(fmt.Sprintf(", **Line:** %d", c.Line))
				}
				b.WriteString("\n\n")
			}
			if c.DiffHunk != "" {
				b.WriteString("**Diff context:**\n```\n")
				b.WriteString(c.DiffHunk)
				b.WriteString("\n```\n\n")
			}
			b.WriteString("**Feedback:**\n")
			b.WriteString(c.Body)
			b.WriteString("\n\n")
		}
	}

	b.WriteString("## Instructions\n\n")
	b.WriteString("Implement ALL requested changes. Human reviewers have highest authority.\n")
	b.WriteString("After making changes, ensure all tests still pass.\n")

	return b.String()
}
