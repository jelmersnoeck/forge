package review

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jelmersnoeck/forge/internal/types"
)

const agentTimeout = 5 * time.Minute

// ReviewRequest carries everything a review run needs.
type ReviewRequest struct {
	Diff        string
	Specs       []types.SpecEntry
	Context     types.ContextBundle
	BaseBranch  string
	CWD         string
	Incremental bool // true when reviewing an incremental diff (cycle 1+)
}

// Orchestrator runs reviewer×provider combinations concurrently.
type Orchestrator struct {
	providers map[string]types.LLMProvider
	reviewers []Reviewer
}

// NewOrchestrator creates an orchestrator with the given providers and reviewers.
func NewOrchestrator(providers map[string]types.LLMProvider, reviewers []Reviewer) *Orchestrator {
	return &Orchestrator{
		providers: providers,
		reviewers: reviewers,
	}
}

// Run executes all reviewer×provider combinations concurrently, emitting events
// as findings arrive, and returns the collected results.
//
//	┌──────────────────────────────────────────┐
//	│  for each reviewer × provider:           │
//	│    goroutine → Chat() → parse → emit     │
//	│                                          │
//	│  waitgroup barrier                       │
//	│  emit summary                            │
//	└──────────────────────────────────────────┘
func (o *Orchestrator) Run(ctx context.Context, req ReviewRequest, emit func(types.OutboundEvent)) []ReviewResult {
	if strings.TrimSpace(req.Diff) == "" {
		emit(types.OutboundEvent{
			ID:        uuid.New().String(),
			Type:      "review_summary",
			Content:   "No diff to review",
			Timestamp: time.Now().Unix(),
		})
		return nil
	}

	totalCombos := len(o.reviewers) * len(o.providers)
	emit(types.OutboundEvent{
		ID:        uuid.New().String(),
		Type:      "review_start",
		Content:   fmt.Sprintf("Starting review: %d reviewers × %d providers (%d agents)", len(o.reviewers), len(o.providers), totalCombos),
		Timestamp: time.Now().Unix(),
	})

	var (
		mu      sync.Mutex
		results []ReviewResult
		wg      sync.WaitGroup
	)

	for _, reviewer := range o.reviewers {
		for providerName, provider := range o.providers {
			wg.Add(1)
			go func(rev Reviewer, pName string, prov types.LLMProvider) {
				defer wg.Done()

				result := o.runSingle(ctx, rev, pName, prov, req, emit)

				mu.Lock()
				results = append(results, result)
				mu.Unlock()

				emit(types.OutboundEvent{
					ID:        uuid.New().String(),
					Type:      "review_agent_done",
					Content:   fmt.Sprintf("Reviewer: %s, Provider: %s, Findings: %d", result.Reviewer, result.Provider, len(result.Findings)),
					Timestamp: time.Now().Unix(),
				})
			}(reviewer, providerName, provider)
		}
	}

	wg.Wait()

	// Emit per-provider summaries before the aggregate.
	for _, pName := range sortedProviderNames(results) {
		var provResults []ReviewResult
		for _, r := range results {
			if r.Provider == pName {
				provResults = append(provResults, r)
			}
		}
		emit(types.OutboundEvent{
			ID:        uuid.New().String(),
			Type:      "review_provider_summary",
			Content:   formatProviderSummary(pName, provResults),
			Timestamp: time.Now().Unix(),
		})
	}

	emit(types.OutboundEvent{
		ID:        uuid.New().String(),
		Type:      "review_summary",
		Content:   formatSummary(results),
		Timestamp: time.Now().Unix(),
	})

	return results
}

// runSingle executes one reviewer against one provider with a timeout.
func (o *Orchestrator) runSingle(
	ctx context.Context,
	rev Reviewer,
	providerName string,
	provider types.LLMProvider,
	req ReviewRequest,
	emit func(types.OutboundEvent),
) ReviewResult {
	agentCtx, cancel := context.WithTimeout(ctx, agentTimeout)
	defer cancel()

	result := ReviewResult{
		Reviewer: rev.Name(),
		Provider: providerName,
	}

	userMessage := buildUserMessage(rev, req)

	chatReq := types.ChatRequest{
		Model: modelForProvider(providerName),
		System: []types.SystemBlock{
			{Type: "text", Text: rev.SystemPrompt()},
		},
		Messages: []types.ChatMessage{
			{
				Role: "user",
				Content: []types.ChatContentBlock{
					{Type: "text", Text: userMessage},
				},
			},
		},
		Tools:     nil,
		MaxTokens: 4096,
		Stream:    true,
	}

	deltaChan, err := provider.Chat(agentCtx, chatReq)
	if err != nil {
		result.Error = fmt.Sprintf("provider error: %v", err)
		emitError(emit, result.Error)
		return result
	}

	responseText := collectResponse(agentCtx, deltaChan)

	findings, err := parseFindings(responseText)
	if err != nil {
		result.Error = fmt.Sprintf("parse error: %v", err)
		emitError(emit, fmt.Sprintf("Reviewer %s/%s: %s", rev.Name(), providerName, result.Error))
		return result
	}

	// Stamp each finding with the reviewer and provider.
	for i := range findings {
		findings[i].Reviewer = rev.Name()
		findings[i].Provider = providerName
	}
	result.Findings = findings

	// Emit individual findings.
	for _, f := range findings {
		fJSON, _ := json.Marshal(f)
		emit(types.OutboundEvent{
			ID:        uuid.New().String(),
			Type:      "review_finding",
			Content:   string(fJSON),
			Timestamp: time.Now().Unix(),
		})
	}

	return result
}

// buildUserMessage constructs the user prompt with the diff and optional specs.
// When the request is incremental, an instruction is injected telling the
// reviewer to only flag issues visible in this diff.
func buildUserMessage(rev Reviewer, req ReviewRequest) string {
	var sb strings.Builder

	if req.Incremental {
		sb.WriteString("You are reviewing an **incremental diff** — only changes since the last review cycle. Flag only issues visible in this diff. Do not speculate about issues in code you cannot see.\n\n")
	}

	sb.WriteString("Please review the following git diff:\n\n```diff\n")
	sb.WriteString(req.Diff)
	sb.WriteString("\n```\n")

	// Include specs for spec-validation reviewer.
	if rev.Name() == "spec-validation" && len(req.Specs) > 0 {
		sb.WriteString("\n## Active Specs\n\n")
		for _, spec := range req.Specs {
			fmt.Fprintf(&sb, "### %s (status: %s)\n\n", spec.ID, spec.Status)
			sb.WriteString(spec.Content)
			sb.WriteString("\n\n")
		}
	}

	return sb.String()
}

// collectResponse drains a delta channel and assembles the full text response.
func collectResponse(ctx context.Context, deltaChan <-chan types.ChatDelta) string {
	var sb strings.Builder
	for {
		select {
		case <-ctx.Done():
			return sb.String()
		case delta, ok := <-deltaChan:
			if !ok {
				return sb.String()
			}
			switch delta.Type {
			case "text_delta":
				sb.WriteString(delta.Text)
			case "error":
				return sb.String()
			}
		}
	}
}

// jsonArrayRe matches a JSON array, possibly embedded in markdown.
var jsonArrayRe = regexp.MustCompile(`\[[\s\S]*\]`)

// parseFindings extracts a JSON array of findings from LLM response text.
// Handles markdown code blocks and surrounding prose.
func parseFindings(text string) ([]Finding, error) {
	text = strings.TrimSpace(text)
	if text == "" || text == "[]" {
		return nil, nil
	}

	// Strip markdown code fences if present.
	stripped := stripCodeFences(text)

	// Try direct unmarshal first.
	var findings []Finding
	if err := json.Unmarshal([]byte(stripped), &findings); err == nil {
		return findings, nil
	}

	// Fallback: regex extract the first JSON array.
	match := jsonArrayRe.FindString(stripped)
	if match == "" {
		match = jsonArrayRe.FindString(text)
	}
	if match == "" {
		return nil, fmt.Errorf("no JSON array found in response")
	}

	if err := json.Unmarshal([]byte(match), &findings); err != nil {
		return nil, fmt.Errorf("invalid JSON in response: %w", err)
	}
	return findings, nil
}

// stripCodeFences removes ```json ... ``` or ``` ... ``` wrappers.
func stripCodeFences(text string) string {
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

// modelForProvider returns the model to use for a given provider name.
func modelForProvider(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "openai"):
		return "gpt-4.1"
	default:
		return "claude-sonnet-4-20250514"
	}
}

func emitError(emit func(types.OutboundEvent), msg string) {
	emit(types.OutboundEvent{
		ID:        uuid.New().String(),
		Type:      "review_error",
		Content:   msg,
		Timestamp: time.Now().Unix(),
	})
}

// sortedProviderNames returns unique provider names from results in sorted order.
func sortedProviderNames(results []ReviewResult) []string {
	seen := map[string]bool{}
	for _, r := range results {
		seen[r.Provider] = true
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// formatProviderSummary builds a per-reviewer breakdown for a single provider.
//
//	Provider: anthropic
//	  security: 1 critical, 2 warnings
//	  code-quality: 1 suggestion
//	  maintainability: (clean)
func formatProviderSummary(providerName string, results []ReviewResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Provider: %s", providerName)

	for _, r := range results {
		sb.WriteString("\n  ")
		sb.WriteString(r.Reviewer)
		sb.WriteString(": ")

		switch {
		case r.Error != "":
			fmt.Fprintf(&sb, "error: %s", r.Error)
		case len(r.Findings) == 0:
			sb.WriteString("(clean)")
		default:
			counts := map[Severity]int{}
			for _, f := range r.Findings {
				counts[f.Severity]++
			}
			var parts []string
			for _, sev := range []Severity{SeverityCritical, SeverityWarning, SeveritySuggestion} {
				if c := counts[sev]; c > 0 {
					parts = append(parts, fmt.Sprintf("%d %s", c, sev))
				}
			}
			sb.WriteString(strings.Join(parts, ", "))
		}
	}
	return sb.String()
}

// FormatFindingsMessage builds a human-readable message listing all findings
// grouped by reviewer+provider, suitable for sending to the main agent loop
// for automatic remediation.
func FormatFindingsMessage(results []ReviewResult) string {
	var sb strings.Builder
	sb.WriteString("The code review found the following issues. Please fix them:\n")

	for _, r := range results {
		if len(r.Findings) == 0 {
			continue
		}

		fmt.Fprintf(&sb, "\n## %s (%s)\n", r.Reviewer, r.Provider)
		for _, f := range r.Findings {
			loc := ""
			if f.File != "" {
				loc = f.File
				if f.StartLine > 0 {
					loc += fmt.Sprintf(":%d", f.StartLine)
				}
				loc += " — "
			}
			fmt.Fprintf(&sb, "- [%s] %s%s\n", f.Severity, loc, f.Description)
		}
	}
	return sb.String()
}

// HasActionableFindings returns true if any result contains findings.
func HasActionableFindings(results []ReviewResult) bool {
	for _, r := range results {
		if len(r.Findings) > 0 {
			return true
		}
	}
	return false
}

// HasHighSeverityFindings returns true if any result contains a critical or warning finding.
func HasHighSeverityFindings(results []ReviewResult) bool {
	for _, r := range results {
		for _, f := range r.Findings {
			switch f.Severity {
			case SeverityCritical, SeverityWarning:
				return true
			}
		}
	}
	return false
}

// FilterHighSeverity returns only critical and warning findings from results.
// Each ReviewResult is preserved but with its findings list filtered.
func FilterHighSeverity(results []ReviewResult) []ReviewResult {
	out := make([]ReviewResult, 0, len(results))
	for _, r := range results {
		filtered := ReviewResult{
			Reviewer: r.Reviewer,
			Provider: r.Provider,
			Error:    r.Error,
		}
		for _, f := range r.Findings {
			switch f.Severity {
			case SeverityCritical, SeverityWarning:
				filtered.Findings = append(filtered.Findings, f)
			}
		}
		out = append(out, filtered)
	}
	return out
}

// HasCriticalFindings returns true if any result contains a critical finding.
func HasCriticalFindings(results []ReviewResult) bool {
	for _, r := range results {
		for _, f := range r.Findings {
			if f.Severity == SeverityCritical {
				return true
			}
		}
	}
	return false
}

// formatSummary builds a human-readable summary of findings by severity.
func formatSummary(results []ReviewResult) string {
	counts := map[Severity]int{}
	errors := 0
	for _, r := range results {
		if r.Error != "" {
			errors++
		}
		for _, f := range r.Findings {
			counts[f.Severity]++
		}
	}

	total := counts[SeverityCritical] + counts[SeverityWarning] + counts[SeveritySuggestion]

	var sb strings.Builder
	fmt.Fprintf(&sb, "Review complete: %d findings", total)
	if counts[SeverityCritical] > 0 {
		fmt.Fprintf(&sb, ", %d critical", counts[SeverityCritical])
	}
	if counts[SeverityWarning] > 0 {
		fmt.Fprintf(&sb, ", %d warnings", counts[SeverityWarning])
	}
	if counts[SeveritySuggestion] > 0 {
		fmt.Fprintf(&sb, ", %d suggestions", counts[SeveritySuggestion])
	}
	if errors > 0 {
		fmt.Fprintf(&sb, " (%d reviewer errors)", errors)
	}
	return sb.String()
}
