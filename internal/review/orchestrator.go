package review

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/principles"
)

// ReviewRequest contains everything needed for a review run.
type ReviewRequest struct {
	Diff          string           // Unified diff to review.
	PrincipleSets []string         // Which principle sets to apply.
	WorkDir       string           // Repository working directory.
	Reviewers     []ReviewerConfig // Reviewer configurations.
}

// ReviewerConfig configures a single reviewer agent.
type ReviewerConfig struct {
	Name          string   // Human-readable reviewer name.
	Agent         string   // Agent backend key (must exist in agent pool).
	PrincipleSets []string // Optional: override principle sets for this reviewer.
}

// Orchestrator coordinates multiple review agents running in parallel.
type Orchestrator struct {
	agents     map[string]agent.Agent
	principles *principles.Store
}

// NewOrchestrator creates a new review Orchestrator.
func NewOrchestrator(agents map[string]agent.Agent, ps *principles.Store) *Orchestrator {
	return &Orchestrator{
		agents:     agents,
		principles: ps,
	}
}

// reviewerResult captures the output from a single reviewer goroutine.
type reviewerResult struct {
	name     string
	findings []Finding
	err      error
}

// Review runs all configured reviewers in parallel, aggregates and deduplicates
// their findings, and returns a unified Result.
func (o *Orchestrator) Review(ctx context.Context, req ReviewRequest) (*Result, error) {
	// Load principles for the review.
	principleSets := req.PrincipleSets
	if len(principleSets) == 0 {
		return nil, fmt.Errorf("review: no principle sets specified")
	}

	allPrinciples, err := o.principles.Load(ctx, principleSets...)
	if err != nil {
		return nil, fmt.Errorf("review: loading principles: %w", err)
	}

	// If no reviewers are configured, return an error.
	if len(req.Reviewers) == 0 {
		return nil, fmt.Errorf("review: no reviewers configured")
	}

	// Run reviewers in parallel.
	results := make(chan reviewerResult, len(req.Reviewers))
	var wg sync.WaitGroup

	for _, rc := range req.Reviewers {
		wg.Add(1)
		go func(rc ReviewerConfig) {
			defer wg.Done()

			ag, ok := o.agents[rc.Agent]
			if !ok {
				results <- reviewerResult{
					name: rc.Name,
					err:  fmt.Errorf("agent %q not found in pool", rc.Agent),
				}
				return
			}

			// Use reviewer-specific principle sets if configured, otherwise use request-level.
			reviewPrinciples := allPrinciples
			if len(rc.PrincipleSets) > 0 {
				rp, err := o.principles.Load(ctx, rc.PrincipleSets...)
				if err != nil {
					results <- reviewerResult{
						name: rc.Name,
						err:  fmt.Errorf("loading principles for reviewer %s: %w", rc.Name, err),
					}
					return
				}
				reviewPrinciples = rp
			}

			// Assemble review prompt.
			prompt := principles.AssembleReviewPrompt(req.Diff, reviewPrinciples)

			// Run the agent.
			resp, err := ag.Run(ctx, agent.Request{
				Prompt:       prompt,
				WorkDir:      req.WorkDir,
				Mode:         agent.ModeReview,
				Permissions:  agent.ToolPermissions{Read: true},
				OutputFormat: "json",
			})
			if err != nil {
				results <- reviewerResult{
					name: rc.Name,
					err:  fmt.Errorf("running reviewer %s: %w", rc.Name, err),
				}
				return
			}

			// Parse findings from agent output.
			findings, parseErr := ParseFindings(resp.Output)
			if parseErr != nil {
				results <- reviewerResult{
					name: rc.Name,
					err:  fmt.Errorf("parsing findings from reviewer %s: %w", rc.Name, parseErr),
				}
				return
			}
			// Tag each finding with the reviewer name.
			for i := range findings {
				findings[i].Reviewer = rc.Name
			}

			results <- reviewerResult{
				name:     rc.Name,
				findings: findings,
			}
		}(rc)
	}

	// Close channel once all goroutines complete.
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect all findings.
	var allFindings []Finding
	var errs []error
	for r := range results {
		if r.err != nil {
			slog.Warn("reviewer failed", "reviewer", r.name, "error", r.err)
			errs = append(errs, r.err)
			continue
		}
		allFindings = append(allFindings, r.findings...)
	}

	// If ALL reviewers failed, return error.
	if len(errs) == len(req.Reviewers) {
		return nil, fmt.Errorf("review: all reviewers failed: %v", errs)
	}

	// Deduplicate findings.
	deduped := Deduplicate(allFindings)

	return &Result{
		Findings:          deduped,
		HasCritical:       HasCriticalFindings(deduped),
		PrinciplesCovered: CoveredPrinciples(deduped),
	}, nil
}
