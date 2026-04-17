package phase

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jelmersnoeck/forge/internal/runtime/loop"
	"github.com/jelmersnoeck/forge/internal/runtime/session"
	"github.com/jelmersnoeck/forge/internal/tools"
	"github.com/jelmersnoeck/forge/internal/types"
)

const debateAgentTimeout = 5 * time.Minute

// Candidate is a proposed approach from an ideation agent.
type Candidate struct {
	Name      string   `json:"name"`
	Summary   string   `json:"summary"`
	Approach  string   `json:"approach"`
	Tradeoffs []string `json:"tradeoffs"`
	Risks     []string `json:"risks"`
	Effort    string   `json:"effort"`
	Reuses    []string `json:"reuses"`
	Source    string   `json:"source"`
}

// ClarifiedResult is the output of the clarifier.
type ClarifiedResult struct {
	Candidates []Candidate `json:"candidates"`
	Questions  []string    `json:"questions,omitempty"`
	Merged     []string    `json:"merged,omitempty"`
}

// DebateResult is the output of the full ideation pipeline.
type DebateResult struct {
	SpecPath     string      `json:"spec_path"`
	Winner       Candidate   `json:"winner"`
	Alternatives []Candidate `json:"alternatives"`
}

// DebateOpts configures a debate run.
type DebateOpts struct {
	Provider     types.LLMProvider
	Registry     *tools.Registry
	Bundle       types.ContextBundle
	CWD          string
	SessionStore *session.Store
	SessionID    string
	Model        string
	Emit         func(types.OutboundEvent)
	AuditLogger  types.AuditLogger
	Prompt       string
}

// ideatorPersonality defines the lens for each ideation agent.
type ideatorPersonality struct {
	Name   string
	System string
}

var ideatorPersonalities = []ideatorPersonality{
	{
		Name: "conservative",
		System: `You are a conservative software architect. Your goal: minimal changes, maximum reuse.

Approach every problem by asking:
- What existing code can I extend instead of creating new code?
- What's the smallest change that fully solves this?
- What patterns does this codebase already use that I should follow?

Avoid new abstractions unless absolutely necessary. Prefer boring, proven approaches.`,
	},
	{
		Name: "pragmatic",
		System: `You are a pragmatic software engineer. Your goal: balanced solutions that are maintainable and clear.

Approach every problem by asking:
- What's the right level of abstraction here?
- Where should I refactor vs. where should I extend?
- What will make this easiest to review and test?

You're comfortable with moderate refactoring when it improves clarity.`,
	},
	{
		Name: "ambitious",
		System: `You are an ambitious software architect. Your goal: clean, forward-looking design.

Approach every problem by asking:
- What's the ideal architecture if I could start fresh?
- What technical debt can I pay down while solving this?
- What extensibility will the team thank me for later?

Don't be reckless — but don't be afraid to propose larger changes when they're clearly better.`,
	},
}

// RunDebate executes the ideation → clarification → planning pipeline.
//
//	User prompt
//	    │
//	    ▼
//	┌──────────────────┐
//	│  3× od-ideate    │  parallel, different personalities
//	└────────┬─────────┘
//	         ▼
//	┌──────────────────┐
//	│  od-clarifier    │  dedupe, gap analysis
//	└────────┬─────────┘
//	         ▼
//	┌──────────────────┐
//	│  od-planner      │  score, select, write spec
//	└────────┬─────────┘
//	         ▼
//	     DebateResult
func RunDebate(ctx context.Context, opts DebateOpts) (DebateResult, error) {
	// Phase 1: Parallel ideation
	opts.Emit(types.OutboundEvent{
		ID:        uuid.New().String(),
		SessionID: opts.SessionID,
		Type:      "ideation_start",
		Content:   fmt.Sprintf("Starting ideation with %d agents", len(ideatorPersonalities)),
		Timestamp: time.Now().UnixMilli(),
	})

	candidates, err := runIdeators(ctx, opts)
	if err != nil {
		return DebateResult{}, fmt.Errorf("ideation phase: %w", err)
	}

	if len(candidates) == 0 {
		return DebateResult{}, fmt.Errorf("ideation produced no candidates")
	}

	// Emit candidates
	for _, c := range candidates {
		cJSON, _ := json.Marshal(c)
		opts.Emit(types.OutboundEvent{
			ID:        uuid.New().String(),
			SessionID: opts.SessionID,
			Type:      "ideation_candidate",
			Content:   string(cJSON),
			Timestamp: time.Now().UnixMilli(),
		})
	}

	// Phase 2: Clarification
	opts.Emit(types.OutboundEvent{
		ID:        uuid.New().String(),
		SessionID: opts.SessionID,
		Type:      "clarification_start",
		Content:   fmt.Sprintf("Clarifying %d candidates", len(candidates)),
		Timestamp: time.Now().UnixMilli(),
	})

	clarified, err := runClarifier(ctx, opts, candidates)
	if err != nil {
		log.Printf("[debate:%s] clarifier error (using raw candidates): %v", opts.SessionID, err)
		clarified = ClarifiedResult{Candidates: candidates}
	}

	// Emit clarification questions (if any)
	for _, q := range clarified.Questions {
		opts.Emit(types.OutboundEvent{
			ID:        uuid.New().String(),
			SessionID: opts.SessionID,
			Type:      "clarification_question",
			Content:   q,
			Timestamp: time.Now().UnixMilli(),
		})
	}

	// Phase 3: Planning — uses a conversation loop with tools (Write)
	opts.Emit(types.OutboundEvent{
		ID:        uuid.New().String(),
		SessionID: opts.SessionID,
		Type:      "planning_start",
		Content:   fmt.Sprintf("Planning from %d refined candidates", len(clarified.Candidates)),
		Timestamp: time.Now().UnixMilli(),
	})

	result, err := runPlanner(ctx, opts, clarified)
	if err != nil {
		return DebateResult{}, fmt.Errorf("planning phase: %w", err)
	}

	opts.Emit(types.OutboundEvent{
		ID:        uuid.New().String(),
		SessionID: opts.SessionID,
		Type:      "planning_selection",
		Content:   fmt.Sprintf("Selected: %s", result.Winner.Name),
		Timestamp: time.Now().UnixMilli(),
	})

	return result, nil
}

// runIdeators runs 3 parallel ideation agents and collects their candidates.
// Uses direct LLM calls (same pattern as review orchestrator).
func runIdeators(ctx context.Context, opts DebateOpts) ([]Candidate, error) {
	ideateCtx, cancel := context.WithTimeout(ctx, debateAgentTimeout)
	defer cancel()

	var (
		mu         sync.Mutex
		candidates []Candidate
		wg         sync.WaitGroup
		errCount   int
	)

	// Build the exploration context summary for ideators.
	contextSummary := buildContextSummary(opts.Bundle)

	for _, personality := range ideatorPersonalities {
		wg.Add(1)
		go func(p ideatorPersonality) {
			defer wg.Done()

			result, err := runSingleIdeator(ideateCtx, opts, p, contextSummary)
			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				errCount++
				log.Printf("[debate:%s] ideator %q failed: %v", opts.SessionID, p.Name, err)
				return
			}
			candidates = append(candidates, result...)
		}(personality)
	}

	wg.Wait()

	// At least 1 agent must succeed.
	if len(candidates) == 0 {
		return nil, fmt.Errorf("all %d ideation agents failed", len(ideatorPersonalities))
	}

	return candidates, nil
}

// runSingleIdeator runs one ideation agent with the given personality.
// Returns structured candidates via JSON in the LLM response.
func runSingleIdeator(ctx context.Context, opts DebateOpts, personality ideatorPersonality, contextSummary string) ([]Candidate, error) {
	systemPrompt := personality.System + "\n\n" + ideatorTaskPrompt

	userMessage := fmt.Sprintf(
		"## User Request\n\n%s\n\n## Project Context\n\n%s",
		opts.Prompt,
		contextSummary,
	)

	req := types.ChatRequest{
		Model: opts.Model,
		System: []types.SystemBlock{
			{Type: "text", Text: systemPrompt},
		},
		Messages: []types.ChatMessage{
			{
				Role: "user",
				Content: []types.ChatContentBlock{
					{Type: "text", Text: userMessage},
				},
			},
		},
		MaxTokens: 4096,
		Stream:    true,
	}

	deltaChan, err := opts.Provider.Chat(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("provider.Chat: %w", err)
	}

	var text strings.Builder
	for delta := range deltaChan {
		switch delta.Type {
		case "text_delta":
			text.WriteString(delta.Text)
		case "error":
			return nil, fmt.Errorf("stream error: %s", delta.Text)
		}
	}

	// Parse candidates from response JSON.
	candidates, err := parseCandidates(text.String(), personality.Name)
	if err != nil {
		return nil, fmt.Errorf("parse candidates: %w", err)
	}

	return candidates, nil
}

// runClarifier deduplicates and refines candidates via a single LLM call.
func runClarifier(ctx context.Context, opts DebateOpts, candidates []Candidate) (ClarifiedResult, error) {
	clarifyCtx, cancel := context.WithTimeout(ctx, debateAgentTimeout)
	defer cancel()

	candidatesJSON, _ := json.MarshalIndent(candidates, "", "  ")

	userMessage := fmt.Sprintf(
		"## Original Request\n\n%s\n\n## Candidate Approaches\n\n```json\n%s\n```",
		opts.Prompt,
		string(candidatesJSON),
	)

	req := types.ChatRequest{
		Model: opts.Model,
		System: []types.SystemBlock{
			{Type: "text", Text: clarifierPrompt},
		},
		Messages: []types.ChatMessage{
			{
				Role: "user",
				Content: []types.ChatContentBlock{
					{Type: "text", Text: userMessage},
				},
			},
		},
		MaxTokens: 4096,
		Stream:    true,
	}

	deltaChan, err := opts.Provider.Chat(clarifyCtx, req)
	if err != nil {
		return ClarifiedResult{}, fmt.Errorf("provider.Chat: %w", err)
	}

	var text strings.Builder
	for delta := range deltaChan {
		switch delta.Type {
		case "text_delta":
			text.WriteString(delta.Text)
		case "error":
			return ClarifiedResult{}, fmt.Errorf("stream error: %s", delta.Text)
		}
	}

	return parseClarifiedResult(text.String())
}

// runPlanner uses a conversation loop (with tools) to score candidates and
// write the spec. This is the only debate phase that writes files.
func runPlanner(ctx context.Context, opts DebateOpts, clarified ClarifiedResult) (DebateResult, error) {
	phase := Planner()
	registry := opts.Registry.Filtered(phase.AllowedTools, phase.DisallowedTools)
	bundle := injectPhasePrompt(opts.Bundle, phase.Name)

	model := opts.Model
	if phase.Model != "" {
		model = phase.Model
	}

	candidatesJSON, _ := json.MarshalIndent(clarified.Candidates, "", "  ")

	plannerMessage := fmt.Sprintf(
		"## User Request\n\n%s\n\n## Refined Candidates\n\n```json\n%s\n```\n\n"+
			"Score each candidate against repo patterns, historic specs in .forge/specs/, "+
			"learnings in .forge/learnings/, effort-vs-value, and risk. "+
			"Select the best approach and write a spec to .forge/specs/ that includes an "+
			"## Alternatives section documenting why each rejected candidate was not chosen. "+
			"Set the spec status to draft.",
		opts.Prompt,
		string(candidatesJSON),
	)

	if len(clarified.Questions) > 0 {
		plannerMessage += "\n\n## Unresolved Questions (resolve using safer interpretation)\n\n"
		for _, q := range clarified.Questions {
			plannerMessage += "- " + q + "\n"
		}
	}

	loopOpts := loop.Options{
		Provider:     opts.Provider,
		Tools:        registry,
		Context:      bundle,
		CWD:          opts.CWD,
		SessionStore: opts.SessionStore,
		SessionID:    opts.SessionID,
		Model:        model,
		MaxTurns:     phase.MaxTurns,
		AuditLogger:  opts.AuditLogger,
	}

	l := loop.New(loopOpts)
	if err := l.Send(ctx, plannerMessage, opts.Emit); err != nil {
		return DebateResult{}, err
	}

	// Find the spec that was just written.
	specPath := findLatestSpec(opts.CWD)

	result := DebateResult{
		SpecPath: specPath,
	}

	// Best-effort: extract winner from candidates list.
	if len(clarified.Candidates) > 0 {
		result.Winner = clarified.Candidates[0]
		if len(clarified.Candidates) > 1 {
			result.Alternatives = clarified.Candidates[1:]
		}
	}

	return result, nil
}

// parseCandidates extracts candidates from an ideator's JSON response.
func parseCandidates(raw, source string) ([]Candidate, error) {
	raw = strings.TrimSpace(raw)

	// Try to extract JSON from markdown code fences.
	stripped := stripJSONFences(raw)

	// Try parsing as a wrapper object: {"candidates": [...]}
	var wrapper struct {
		Candidates []Candidate `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(stripped), &wrapper); err == nil && len(wrapper.Candidates) > 0 {
		for i := range wrapper.Candidates {
			wrapper.Candidates[i].Source = source
		}
		return wrapper.Candidates, nil
	}

	// Try parsing as a direct array.
	var candidates []Candidate
	if err := json.Unmarshal([]byte(stripped), &candidates); err == nil && len(candidates) > 0 {
		for i := range candidates {
			candidates[i].Source = source
		}
		return candidates, nil
	}

	return nil, fmt.Errorf("no valid candidates JSON found in response")
}

// parseClarifiedResult extracts the clarifier's structured output.
func parseClarifiedResult(raw string) (ClarifiedResult, error) {
	raw = strings.TrimSpace(raw)
	stripped := stripJSONFences(raw)

	var result ClarifiedResult
	if err := json.Unmarshal([]byte(stripped), &result); err != nil {
		return ClarifiedResult{}, fmt.Errorf("parse clarified result: %w — raw: %q", err, truncateForLog(raw, 200))
	}

	return result, nil
}

// stripJSONFences removes ```json ... ``` or ``` ... ``` wrappers.
func stripJSONFences(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) < 2 {
		return text
	}

	first := strings.TrimSpace(lines[0])
	if strings.HasPrefix(first, "```") {
		last := strings.TrimSpace(lines[len(lines)-1])
		if last == "```" {
			return strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	return text
}

// buildContextSummary creates a compact context string from the bundle
// for ideation agents (they don't have tools to explore the repo themselves
// in the direct-LLM call variant).
func buildContextSummary(bundle types.ContextBundle) string {
	var sb strings.Builder

	// Include AGENTS.md content (truncated).
	for _, entry := range bundle.AgentsMD {
		if entry.Level == "project" || entry.Level == "user" {
			fmt.Fprintf(&sb, "## %s (%s)\n\n", entry.Path, entry.Level)
			content := entry.Content
			if len(content) > 3000 {
				content = content[:3000] + "\n... (truncated)"
			}
			sb.WriteString(content)
			sb.WriteString("\n\n")
		}
	}

	// Include active spec summaries.
	if len(bundle.Specs) > 0 {
		sb.WriteString("## Active Specs\n\n")
		for _, spec := range bundle.Specs {
			if spec.Status == "active" || spec.Status == "draft" {
				fmt.Fprintf(&sb, "- **%s** (%s): %s\n", spec.ID, spec.Status, spec.Header)
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// ideatorTaskPrompt is appended to each ideator's personality prompt.
const ideatorTaskPrompt = `Analyze the user's request and produce 1-2 candidate approaches.

Output ONLY a JSON object with this structure (no markdown, no prose):

{"candidates": [
  {
    "name": "short-kebab-name",
    "summary": "2-3 sentence description of the approach",
    "approach": "Detailed technical approach: what files change, what types are added, what the data flow looks like",
    "tradeoffs": ["advantage 1", "advantage 2"],
    "risks": ["risk or downside 1", "risk or downside 2"],
    "effort": "S|M|L|XL",
    "reuses": ["existing-type-or-pattern-it-builds-on"]
  }
]}

Guidelines:
- Each candidate must be concretely different (not just naming variations)
- Reference specific files, types, and functions from the project context
- Effort scale: S = hours, M = 1-2 days, L = 3-5 days, XL = 1+ weeks
- Be honest about risks — every approach has them`

// clarifierPrompt is the system prompt for the clarification agent.
const clarifierPrompt = `You are a technical clarifier. You receive candidate approaches from multiple
ideation agents and your job is to refine them.

Your responsibilities:
1. **Deduplicate**: if two candidates are >80% similar in approach, merge them
   (keep the better-articulated version, note the merge)
2. **Gap analysis**: check if any candidate misses a key constraint mentioned
   in the project context
3. **Conflict detection**: flag if candidates make incompatible assumptions
4. **Clarifying questions**: if there's genuine ambiguity that affects which
   approach is better, list questions. Keep questions specific and actionable.

Output ONLY a JSON object (no markdown, no prose):

{
  "candidates": [<refined candidate objects, same schema as input>],
  "questions": ["question 1", "question 2"],
  "merged": ["name-of-merged-candidate-1"]
}

Rules:
- Preserve candidate names and sources
- Don't invent new candidates — only refine existing ones
- If no duplicates exist, return candidates unchanged
- If no questions exist, omit the questions field
- Maximum 3 questions (prioritize by impact on decision)`
