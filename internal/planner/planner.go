package planner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/tracker"
	"gopkg.in/yaml.v3"
)

// Planner decomposes high-level goals into workstreams with phases and issues.
// It uses an agent for goal decomposition and a tracker for issue creation.
type Planner struct {
	agent   agent.Agent
	tracker tracker.Tracker
}

// PlanRequest contains the inputs needed for goal decomposition.
type PlanRequest struct {
	Goal         string   // High-level goal description.
	Tracker      string   // Tracker backend name (e.g., "github", "jira").
	Repo         string   // Repository for issue creation.
	ContextFiles []string // Paths to context files to include in the prompt.
}

// New creates a new Planner with the given agent and tracker.
func New(agent agent.Agent, tracker tracker.Tracker) *Planner {
	return &Planner{
		agent:   agent,
		tracker: tracker,
	}
}

// Plan decomposes a high-level goal into a structured workstream with phases
// and issues. It sends the goal (with optional context files) to the agent
// and parses the structured YAML output into a Workstream.
func (p *Planner) Plan(ctx context.Context, req PlanRequest) (*Workstream, error) {
	slog.Info("starting goal decomposition", "goal_length", len(req.Goal))

	if req.Goal == "" {
		return nil, fmt.Errorf("plan: goal is required")
	}

	// Build the planning prompt.
	prompt := buildPlanPrompt(req)

	// Load context files if provided.
	for _, path := range req.ContextFiles {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading context file %s: %w", path, err)
		}
		prompt += fmt.Sprintf("\n\n## Context: %s\n\n```\n%s\n```\n", path, string(content))
	}

	// Run the agent in plan mode.
	resp, err := p.agent.Run(ctx, agent.Request{
		Prompt:  prompt,
		WorkDir: ".",
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

	// Parse the structured output into a workstream.
	ws, err := parseWorkstreamOutput(resp.Output)
	if err != nil {
		return nil, fmt.Errorf("parsing plan output: %w", err)
	}

	// Set metadata.
	ws.Tracker = req.Tracker
	ws.Repo = req.Repo
	ws.CreatedAt = time.Now()
	ws.Status = StatusPending

	// Default all issue statuses to pending.
	for i := range ws.Phases {
		for j := range ws.Phases[i].Issues {
			ws.Phases[i].Issues[j].Status = StatusPending
		}
	}

	slog.Info("goal decomposition complete",
		"workstream_id", ws.ID,
		"phases", len(ws.Phases),
		"total_issues", len(ws.AllIssues()),
	)

	return ws, nil
}

// buildPlanPrompt constructs the planning prompt for goal decomposition.
func buildPlanPrompt(req PlanRequest) string {
	return fmt.Sprintf(`You are a software architect decomposing a project goal into an implementation plan.

## Goal

%s

## Instructions

Break this goal down into a workstream with phases and issues. Each phase groups related work that can potentially be done in parallel. Issues within a phase may have dependencies on issues in earlier phases.

Output your plan as a YAML document inside a yaml code block with the following structure:

%s

Guidelines:
- Create 2-5 phases with clear boundaries
- Each issue should be small enough for a single agent session
- Use depends_on to reference other issue titles within the workstream
- Include labels like "feature", "refactor", "test", "docs" as appropriate
- Issue descriptions should be detailed enough for an agent to implement
- The workstream ID should be a short, descriptive slug (e.g., "ws-auth-system")
`, req.Goal, "```yaml\nid: ws-<short-id>\ngoal: \"<the goal>\"\nphases:\n  - name: \"<phase name>\"\n    issues:\n      - title: \"<issue title>\"\n        description: \"<detailed description of what to implement>\"\n        labels:\n          - \"<label>\"\n        depends_on:\n          - \"<title of dependency issue>\"\n      - title: \"<another issue>\"\n        description: \"<description>\"\n```")
}

// parseWorkstreamOutput extracts a Workstream from the agent's YAML output.
// The output may contain a YAML block fenced with ```yaml ... ``` or be
// raw YAML.
func parseWorkstreamOutput(output string) (*Workstream, error) {
	yamlContent := extractYAMLBlock(output)
	if yamlContent == "" {
		// Try parsing the entire output as YAML.
		yamlContent = output
	}

	var ws Workstream
	if err := yaml.Unmarshal([]byte(yamlContent), &ws); err != nil {
		return nil, fmt.Errorf("parsing YAML output: %w", err)
	}

	if ws.ID == "" {
		return nil, fmt.Errorf("agent output missing required 'id' field")
	}
	if ws.Goal == "" {
		return nil, fmt.Errorf("agent output missing required 'goal' field")
	}
	if len(ws.Phases) == 0 {
		return nil, fmt.Errorf("agent output contains no phases")
	}

	return &ws, nil
}

// extractYAMLBlock finds and extracts the content of a ```yaml ... ``` block.
func extractYAMLBlock(text string) string {
	lines := strings.Split(text, "\n")
	var yamlLines []string
	inBlock := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inBlock && (trimmed == "```yaml" || trimmed == "```yml") {
			inBlock = true
			continue
		}
		if inBlock && trimmed == "```" {
			break
		}
		if inBlock {
			yamlLines = append(yamlLines, line)
		}
	}

	if len(yamlLines) == 0 {
		return ""
	}
	return strings.Join(yamlLines, "\n")
}
