package engine

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/tracker"
)

// Plan runs the plan agent on an issue and returns a structured plan
// for human approval. This is the first phase of the governed build loop
// and can also be used standalone via `forge plan`.
func (e *Engine) Plan(ctx context.Context, req PlanRequest) (*PlanResult, error) {
	slog.Info("starting plan phase", "issue_ref", req.IssueRef)

	// Resolve the issue reference.
	ref, err := tracker.ParseIssueRef(req.IssueRef)
	if err != nil {
		return nil, fmt.Errorf("parsing issue reference: %w", err)
	}

	// Determine which tracker to use.
	trackerName := ref.Tracker
	if trackerName == "" {
		trackerName = e.Config.DefaultTracker
	}
	t, ok := e.Trackers[trackerName]
	if !ok {
		return nil, fmt.Errorf("tracker %q not configured", trackerName)
	}

	// Fetch the issue.
	issue, err := t.GetIssue(ctx, req.IssueRef)
	if err != nil {
		return nil, fmt.Errorf("fetching issue %s: %w", req.IssueRef, err)
	}

	// Load principles for prompt assembly.
	principlePrompt := ""
	if e.Principles != nil && len(req.PrincipleSets) > 0 {
		ps, err := e.Principles.Load(ctx, req.PrincipleSets...)
		if err != nil {
			return nil, fmt.Errorf("loading principles: %w", err)
		}
		if len(ps) > 0 {
			principlePrompt = "\n\nConsider these principles when planning:\n"
			for _, p := range ps {
				principlePrompt += fmt.Sprintf("- [%s] %s: %s\n", p.ID, p.Title, p.Description)
			}
		}
	}

	// Assemble the plan prompt.
	prompt := fmt.Sprintf(
		"You are a software architect creating an implementation plan.\n\n"+
			"## Issue\n\n"+
			"**Title:** %s\n\n"+
			"**Description:**\n%s\n"+
			"%s\n\n"+
			"## Instructions\n\n"+
			"Create a detailed implementation plan for this issue. Include:\n"+
			"1. Files to create or modify\n"+
			"2. Key implementation steps\n"+
			"3. Testing approach\n"+
			"4. Potential risks or concerns\n\n"+
			"Output the plan as structured text.",
		issue.Title,
		issue.Body,
		principlePrompt,
	)

	// Get the planner agent.
	plannerAgent, err := e.getAgent(e.Config.PlannerAgent)
	if err != nil {
		return nil, fmt.Errorf("getting planner agent: %w", err)
	}

	// Run the plan agent with read-only permissions.
	resp, err := plannerAgent.Run(ctx, agent.Request{
		Prompt:  prompt,
		WorkDir: req.WorkDir,
		Mode:    agent.ModePlan,
		Permissions: agent.ToolPermissions{
			Read:    true,
			Write:   false,
			Execute: false,
			Network: false,
		},
		OutputFormat: "text",
	})
	if err != nil {
		return nil, fmt.Errorf("running plan agent: %w", err)
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("plan agent error: %s", resp.Error)
	}

	slog.Info("plan phase complete", "issue_ref", req.IssueRef)

	return &PlanResult{
		Plan:     resp.Output,
		Approved: false, // Approval happens externally (CLI or API).
	}, nil
}

// getAgent retrieves an agent by name from the engine's agent map.
func (e *Engine) getAgent(name string) (agent.Agent, error) {
	a, ok := e.Agents[name]
	if !ok {
		return nil, fmt.Errorf("agent %q not configured", name)
	}
	return a, nil
}
