package review

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
// as findings arrive, consolidates duplicates via LLM, and returns results.
//
//	┌──────────────────────────────────────────┐
//	│  for each reviewer × provider:           │
//	│    goroutine → Chat() → parse → emit     │
//	│                                          │
//	│  waitgroup barrier                       │
//	│  consolidate (LLM dedup)                 │
//	│  emit review_consolidated + summary      │
//	└──────────────────────────────────────────┘
func (o *Orchestrator) Run(ctx context.Context, req ReviewRequest, emit func(types.OutboundEvent)) ConsolidatedResults {
	if strings.TrimSpace(req.Diff) == "" {
		emit(types.OutboundEvent{
			ID:        uuid.New().String(),
			Type:      "review_summary",
			Content:   "No diff to review",
			Timestamp: time.Now().Unix(),
		})
		return ConsolidatedResults{}
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

	// Consolidation: deduplicate and calibrate findings via LLM.
	consolidated := o.consolidate(ctx, results, emit)

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

	return ConsolidatedResults{
		Raw:          results,
		Consolidated: consolidated,
	}
}

// consolidate runs the LLM consolidation step across all available providers
// and merges findings. Using multiple providers reduces bias in the
// deduplication/calibration step. Returns raw findings converted 1:1 if
// consolidation is skipped or all providers fail.
func (o *Orchestrator) consolidate(ctx context.Context, results []ReviewResult, emit func(types.OutboundEvent)) []ConsolidatedFinding {
	allFindings := collectAllFindings(results)
	if len(allFindings) == 0 {
		return nil
	}

	providerPairs := o.consolidationProviders()
	if len(providerPairs) == 0 {
		log.Printf("[orchestrator] no provider available for consolidation, using raw findings")
		emit(types.OutboundEvent{
			ID:        uuid.New().String(),
			Type:      "review_error",
			Content:   "Consolidation skipped: no LLM provider available, using raw findings",
			Timestamp: time.Now().Unix(),
		})
		return fallbackToRaw(results)
	}

	// Run consolidation against each provider concurrently.
	// Results are collected via a channel with context-aware draining
	// to prevent goroutine leaks if the parent context is cancelled.
	resultCh := make(chan consolidationResult, len(providerPairs))
	for _, pp := range providerPairs {
		go func(name string, prov types.LLMProvider) {
			model := modelForProvider(name)
			consolidateCtx, cancel := context.WithTimeout(ctx, consolidationTimeout)
			defer cancel()

			consolidated, err := Consolidate(consolidateCtx, prov, model, results)
			select {
			case resultCh <- consolidationResult{
				providerName: name,
				findings:     consolidated,
				err:          err,
			}:
			case <-ctx.Done():
				// Parent cancelled — nobody is reading resultCh. Log and bail
				// to avoid goroutine leak.
				if err != nil {
					log.Printf("[orchestrator] consolidation via %s failed (context cancelled): %v", name, err)
				}
			}
		}(pp.name, pp.provider)
	}

	all := make([]consolidationResult, 0, len(providerPairs))
	for range providerPairs {
		select {
		case cr := <-resultCh:
			all = append(all, cr)
		case <-ctx.Done():
			log.Printf("[orchestrator] context cancelled while collecting consolidation results (%d/%d collected)", len(all), len(providerPairs))
		}
		// Break out of the range loop if context is done.
		if ctx.Err() != nil {
			break
		}
	}

	best, successCount := selectBestConsolidation(all)
	for _, cr := range all {
		if cr.err != nil {
			log.Printf("[orchestrator] consolidation via %s failed: %v", cr.providerName, cr.err)
			emit(types.OutboundEvent{
				ID:        uuid.New().String(),
				Type:      "review_error",
				Content:   fmt.Sprintf("Consolidation via %s failed: %v", cr.providerName, cr.err),
				Timestamp: time.Now().Unix(),
			})
		}
	}

	if successCount == 0 {
		log.Printf("[orchestrator] all consolidation providers failed, using raw findings")
		emit(types.OutboundEvent{
			ID:        uuid.New().String(),
			Type:      "review_error",
			Content:   "All consolidation providers failed — using raw findings",
			Timestamp: time.Now().Unix(),
		})
		return fallbackToRaw(results)
	}

	// Emit the consolidated findings as a single event.
	cJSON, _ := json.Marshal(best)
	emit(types.OutboundEvent{
		ID:        uuid.New().String(),
		Type:      "review_consolidated",
		Content:   string(cJSON),
		Timestamp: time.Now().Unix(),
	})

	return best
}

// consolidationScore computes a weighted score for a set of consolidated
// findings: critical=3, warning=2, suggestion=1, unknown=0. Higher scores
// indicate more important findings, preventing noisy providers from winning
// by volume alone.
func consolidationScore(findings []ConsolidatedFinding) int {
	score := 0
	for _, f := range findings {
		rank := severityRank(f.Severity)
		if rank == 0 && f.Severity != "" {
			log.Printf("[orchestrator] unknown severity %q in consolidated finding, treating as weight 0", f.Severity)
		}
		score += rank
	}
	return score
}

// selectBestConsolidation picks the provider whose findings have the highest
// total severity weight. On ties, the provider with more findings wins (more
// coverage at equal quality). Returns nil and 0 if all results errored.
//
//	Score     = sum(severityRank(f.Severity)) for each finding
//	Tiebreak  = len(findings) — more coverage at equal quality
func selectBestConsolidation(results []consolidationResult) ([]ConsolidatedFinding, int) {
	var (
		best      []ConsolidatedFinding
		bestScore int
		count     int
	)
	for _, cr := range results {
		if cr.err != nil {
			continue
		}
		count++
		score := consolidationScore(cr.findings)
		switch {
		case score > bestScore:
			best = cr.findings
			bestScore = score
		case score == bestScore && len(cr.findings) > len(best):
			best = cr.findings
			bestScore = score
		}
	}
	return best, count
}

// consolidationResult holds one provider's consolidation output or error.
type consolidationResult struct {
	providerName string
	findings     []ConsolidatedFinding
	err          error
}

// providerPair pairs a name with its LLMProvider for consolidation dispatch.
type providerPair struct {
	name     string
	provider types.LLMProvider
}

// consolidationProviders returns all available providers for consolidation.
// Using multiple providers reduces bias in the deduplication step.
func (o *Orchestrator) consolidationProviders() []providerPair {
	pairs := make([]providerPair, 0, len(o.providers))
	for name, p := range o.providers {
		pairs = append(pairs, providerPair{name: name, provider: p})
	}
	// Sort for deterministic ordering.
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].name < pairs[j].name
	})
	return pairs
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
