package phase

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jelmersnoeck/forge/internal/credentials"
	"github.com/jelmersnoeck/forge/internal/review"
	"github.com/jelmersnoeck/forge/internal/runtime/loop"
	"github.com/jelmersnoeck/forge/internal/runtime/provider"
	"github.com/jelmersnoeck/forge/internal/runtime/session"
	"github.com/jelmersnoeck/forge/internal/tools"
	"github.com/jelmersnoeck/forge/internal/types"
)

const maxReviewCycles = 5

// CollectReviewProviders builds the available-providers map for code review,
// keyed by canonical provider name (anthropic, openai, claude-cli). It is the
// single source of review providers, consumed by both worker.runReview and the
// phase orchestrator's reviewer. A provider absent here yields no reviewers for
// that backend (skipped) rather than a fallback.
func CollectReviewProviders() map[string]types.LLMProvider {
	providers := make(map[string]types.LLMProvider)
	if key, ok := credentials.Default().Get(credentials.AnthropicAPIKey); ok {
		providers["anthropic"] = provider.NewAnthropic(key)
	}
	if key, ok := credentials.Default().Get(credentials.OpenAIAPIKey); ok {
		providers["openai"] = provider.NewOpenAI(key)
	}
	if _, err := exec.LookPath("claude"); err == nil {
		providers["claude-cli"] = provider.NewClaudeCLI()
	}
	return providers
}

// Orchestrator chains phases together into a complete workflow.
type Orchestrator struct {
	maxReviewCycles int

	// runMultiPhase is the multi-phase coordinator. Defaults to RunMultiPhase;
	// overridden in tests to avoid touching git/gh.
	runMultiPhase func(ctx context.Context, opts MultiPhaseOpts) error
}

// OrchestratorOpts configures an orchestrator run.
type OrchestratorOpts struct {
	Provider      types.LLMProvider
	Registry      *tools.Registry
	Bundle        types.ContextBundle
	CWD           string
	SessionStore  *session.Store
	SessionID     string
	Model         string
	Emit          func(types.OutboundEvent)
	AuditLogger   types.AuditLogger
	InitialPrompt string
	SpecPath      string // if set, skip spec-creator phase

	// QAHistoryID, when set, resumes an existing Q&A conversation.
	QAHistoryID string

	// InvestigateHistoryID, when set, resumes an existing investigation conversation.
	InvestigateHistoryID string

	// TriageHistoryID, when set, resumes an existing triage conversation.
	TriageHistoryID string

	// PipelineHint controls whether to run the ideation pipeline.
	// Values: "ideate" (always ideate), "spec" (always single-agent spec
	// creator), "code" (skip to coding), "auto" or "" (use complexity gate
	// to decide).
	PipelineHint string

	// ForceTask, when true, skips intent classification entirely and treats
	// the prompt as an actionable task. Used for issue-driven sessions where
	// the intent is unambiguous (the user asked to implement an issue), so
	// the agent must not drift into Q&A, investigation, or review.
	ForceTask bool

	// TransitionHistoryID carries the conversation history from a prior
	// Q&A or investigation phase into the next phase (spec-creator or
	// direct coder). Set during Q&A→task or investigate→task transitions
	// so the spec-creator can Resume() with full prior context.
	TransitionHistoryID string

	// SteeringSource is called between LLM iterations to check for
	// mid-turn user messages. Passed through to loop.Options.
	SteeringSource func() (string, bool)

	// MultiPhase enables sub-issue multi-phase routing on the large-task
	// path (issue #224). When set and the run classifies as a large task,
	// the orchestrator decomposes the parent issue into sub-issues (if none
	// exist yet) and runs each as its own worktree/branch/PR via
	// RunMultiPhase. When false, large tasks use the normal ideate pipeline.
	MultiPhase bool

	// IssueNumber is the parent GitHub issue number for the session. Used to
	// fetch existing sub-issues and to attach newly decomposed ones.
	IssueNumber int

	// IssueTitle is the parent issue title, slugified into phase branch names.
	IssueTitle string

	// IssueBody is the parent issue body, fed to Decompose when no sub-issues
	// exist yet.
	IssueBody string

	// RepoRoot is the git repository root multi-phase worktrees branch from.
	// Defaults to CWD when empty.
	RepoRoot string

	// WorktreeBase is the directory under which per-phase worktrees are
	// created. Defaults to /tmp/forge/worktrees when empty.
	WorktreeBase string

	// SubIssuesFn fetches the parent's sub-issues. Defaults to a gh-backed
	// fetcher when nil. Injected for tests.
	SubIssuesFn SubIssuesFunc

	// CreateSubIssuesFn creates GitHub sub-issues from decomposed tasks.
	// Defaults to CreateSubIssues when nil. Injected for tests.
	CreateSubIssuesFn CreateSubIssuesFunc

	// SpawnPhaseAgentFn runs a full-access sub-agent for one phase, blocking
	// until completion. Required for multi-phase runs; wired by the worker.
	SpawnPhaseAgentFn SpawnPhaseAgent
}

// maxParentSlugLen caps the slugified parent-issue title used in phase branch
// names.
const maxParentSlugLen = 40

// SubIssuesFunc fetches a parent's sub-issues as SubIssue values, in position
// order. Returns (nil, nil) when the parent has no sub-issues.
type SubIssuesFunc func(ctx context.Context, cwd string, parentNumber int) ([]SubIssue, error)

// CreateSubIssuesFunc creates GitHub sub-issues from decomposed tasks and
// returns the created issue numbers in task order.
type CreateSubIssuesFunc func(ctx context.Context, parent int, repo string, tasks []SubTask) ([]int, error)

// OrchestratorResult is the return value from Orchestrator.Run.
type OrchestratorResult struct {
	// Intent is the classified intent for this run.
	Intent Intent
	// QAHistoryID is set when Intent == IntentQuestion.
	// The caller uses this to resume the Q&A loop on follow-up.
	QAHistoryID string
	// InvestigateHistoryID is set when Intent == IntentInvestigate.
	// The caller uses this to resume the investigation loop on follow-up.
	InvestigateHistoryID string
	// TriageHistoryID is set when Intent == IntentTriage.
	// The caller uses this to resume the triage loop on follow-up.
	TriageHistoryID string
	// CoderHistoryID is set after the SWE pipeline completes.
	// The caller uses this to resume the coder conversation for follow-ups.
	CoderHistoryID string
	// SpecHistoryID is set after spec creation (planner or spec-creator).
	// Available for extension (e.g., re-running the spec phase).
	SpecHistoryID string
}

// NewSWEOrchestrator creates the default software-engineer orchestrator.
func NewSWEOrchestrator() *Orchestrator {
	return &Orchestrator{
		maxReviewCycles: maxReviewCycles,
	}
}

// Run executes the SWE pipeline with intent classification.
//
// When no spec is provided, it first classifies the user's intent:
//   - question    → runs Q&A loop, returns result with QAHistoryID
//   - investigate → runs investigation loop, returns result with InvestigateHistoryID
//   - review      → runs review only, returns result with IntentReview
//   - task        → runs SWE pipeline (routing depends on task size)
//
// When SpecPath is set, classification is skipped (intent is unambiguously task).
//
//	User prompt
//	    │
//	    ▼
//	┌───────────────┐
//	│   Classify     │──▶ question?    → Q&A loop → return
//	│                │──▶ investigate? → investigate loop → return
//	│                │──▶ review?      → review only → return
//	└───────┬───────┘
//	        │ task
//	        ▼
//	┌─────────────────────────────────────────────┐
//	│  small  → direct code (no spec, no review)  │
//	│  standard → spec → code → review            │
//	│  large  → ideate → spec → code → review     │
//	└─────────────────────────────────────────────┘
func (o *Orchestrator) Run(ctx context.Context, opts OrchestratorOpts) (OrchestratorResult, error) {
	specPath := opts.SpecPath

	// Classify intent (skip if spec is provided — that's unambiguously a task).
	var classification Classification
	if specPath == "" {
		var err error
		classification, err = Classify(ctx, opts.Provider, opts.InitialPrompt, opts.Bundle.Specs)
		if err != nil {
			log.Printf("[orchestrator:%s] classification error (defaulting to task/standard): %v", opts.SessionID, err)
			opts.Emit(types.OutboundEvent{
				ID:        uuid.New().String(),
				SessionID: opts.SessionID,
				Type:      "classification_error",
				Content:   fmt.Sprintf("classification failed (defaulting to task): %v", err),
				Timestamp: time.Now().UnixMilli(),
			})
		}

		opts.Emit(types.OutboundEvent{
			ID:        uuid.New().String(),
			SessionID: opts.SessionID,
			Type:      "intent_classified",
			Content:   fmt.Sprintf(`{"intent":"%s","size":"%s","spec_match":"%s"}`, classification.Intent, classification.Size, classification.SpecMatch),
			Timestamp: time.Now().UnixMilli(),
		})

		if classification.Intent == IntentQuestion && !opts.ForceTask {
			historyID, err := o.runQA(ctx, opts)
			return OrchestratorResult{
				Intent:      IntentQuestion,
				QAHistoryID: historyID,
			}, err
		}

		if classification.Intent == IntentInvestigate && !opts.ForceTask {
			historyID, err := o.runInvestigate(ctx, opts)
			return OrchestratorResult{
				Intent:               IntentInvestigate,
				InvestigateHistoryID: historyID,
			}, err
		}

		if classification.Intent == IntentTriage && !opts.ForceTask {
			historyID, err := o.runTriage(ctx, opts)
			return OrchestratorResult{
				Intent:          IntentTriage,
				TriageHistoryID: historyID,
			}, err
		}

		if classification.Intent == IntentReview && !opts.ForceTask {
			o.emitPhaseStart(opts, "review")
			_, err := o.runReviewer(ctx, opts, opts.SpecPath)
			return OrchestratorResult{Intent: IntentReview}, err
		}
	}

	// Task path: augment prompt with prior context if transitioning from Q&A or investigation.
	// Carry the conversation history ID so the spec-creator (or direct coder)
	// can Resume() from it, giving it full context — not just the text hint.
	switch {
	case opts.InvestigateHistoryID != "":
		augmented := "Based on our previous investigation, the user now wants to implement: " +
			opts.InitialPrompt + ". Use the context from the investigation to inform the spec."
		log.Printf("[orchestrator:%s] investigate→task transition, augmented prompt (%d chars)", opts.SessionID, len(augmented))
		opts.InitialPrompt = augmented
		opts.TransitionHistoryID = opts.InvestigateHistoryID
		opts.InvestigateHistoryID = ""
	case opts.TriageHistoryID != "":
		augmented := "Based on our previous triage, the user now wants to implement: " +
			opts.InitialPrompt + ". Use the context from the triage to inform the spec."
		log.Printf("[orchestrator:%s] triage→task transition, augmented prompt (%d chars)", opts.SessionID, len(augmented))
		opts.InitialPrompt = augmented
		opts.TransitionHistoryID = opts.TriageHistoryID
		opts.TriageHistoryID = ""
	case opts.QAHistoryID != "":
		augmented := "Based on our previous discussion, the user now wants to implement: " +
			opts.InitialPrompt + ". Use the context from the conversation to inform the spec."
		log.Printf("[orchestrator:%s] Q&A→task transition, augmented prompt (%d chars)", opts.SessionID, len(augmented))
		opts.InitialPrompt = augmented
		opts.TransitionHistoryID = opts.QAHistoryID
		opts.QAHistoryID = ""
	}

	// Spec match injection: hint the spec-creator to update an existing spec.
	if classification.SpecMatch != "" && specPath == "" {
		for _, s := range opts.Bundle.Specs {
			if s.ID == classification.SpecMatch {
				opts.InitialPrompt += fmt.Sprintf(
					"\n\n[Classifier matched existing spec %q (%s) — update it rather than creating a new one.]",
					s.ID, s.Path,
				)
				log.Printf("[orchestrator:%s] injected spec match hint: %s (%s)", opts.SessionID, s.ID, s.Path)
				break
			}
		}
	}

	// Run SWE pipeline with classification for size-based routing.
	return o.runSWEPipeline(ctx, opts, specPath, classification)
}

// runQA runs the Q&A conversation loop. Returns the history ID for resumption.
func (o *Orchestrator) runQA(ctx context.Context, opts OrchestratorOpts) (string, error) {
	return o.runConversationPhase(ctx, opts, QA(), opts.QAHistoryID)
}

// runInvestigate runs the investigation conversation loop. Returns the history ID for resumption.
func (o *Orchestrator) runInvestigate(ctx context.Context, opts OrchestratorOpts) (string, error) {
	return o.runConversationPhase(ctx, opts, Investigate(), opts.InvestigateHistoryID)
}

// runTriage runs the triage conversation loop: investigate, then file a GitHub
// issue via gh. Returns the history ID for resumption.
func (o *Orchestrator) runTriage(ctx context.Context, opts OrchestratorOpts) (string, error) {
	return o.runConversationPhase(ctx, opts, Triage(), opts.TriageHistoryID)
}

// runConversationPhase runs a conversation loop for a given phase config,
// resuming from historyID if non-empty. Returns the resulting history ID.
func (o *Orchestrator) runConversationPhase(ctx context.Context, opts OrchestratorOpts, p Phase, historyID string) (string, error) {
	registry := opts.Registry.Filtered(p.AllowedTools, p.DisallowedTools)
	bundle := InjectPhasePrompt(opts.Bundle, p.Name)

	l := loop.New(loop.Options{
		Provider:       opts.Provider,
		Tools:          registry,
		Context:        bundle,
		CWD:            opts.CWD,
		SessionStore:   opts.SessionStore,
		SessionID:      opts.SessionID,
		Model:          opts.Model,
		MaxTurns:       p.MaxTurns,
		AuditLogger:    opts.AuditLogger,
		SteeringSource: opts.SteeringSource,
	})

	var err error
	switch historyID {
	case "":
		err = l.Send(ctx, opts.InitialPrompt, opts.Emit)
	default:
		err = l.Resume(ctx, historyID, opts.InitialPrompt, opts.Emit)
	}

	return l.HistoryID(), err
}

// runSWEPipeline runs the SWE pipeline with size-based routing.
func (o *Orchestrator) runSWEPipeline(ctx context.Context, opts OrchestratorOpts, specPath string, classification Classification) (OrchestratorResult, error) {
	result := OrchestratorResult{Intent: IntentTask}

	// Multi-phase routing (#224): a large task with the multi-phase signal set
	// decomposes the parent into sub-issues (if none exist) and runs each as
	// its own worktree/branch/PR. A spec path or non-large size falls through
	// to the normal size-based pipeline.
	if opts.MultiPhase && specPath == "" && classification.Size == TaskSizeLarge {
		return o.runMultiPhasePipeline(ctx, opts)
	}

	// Size-based routing: small tasks skip spec and review entirely.
	skipSpec, useIdeation := resolveTaskPipeline(opts.PipelineHint, classification.Size)
	if skipSpec && specPath == "" {
		// Small task path: direct code, no spec, no review.
		coderHistoryID, err := o.runCoderDirect(ctx, opts)
		result.CoderHistoryID = coderHistoryID
		return result, err
	}

	// Phase 1: Spec Creation (skipped if spec already provided)
	if specPath == "" {
		switch useIdeation {
		case true:
			// Full ideation pipeline: ideate → clarify → plan
			o.emitPhaseStart(opts, "ideate")

			debateResult, err := RunDebate(ctx, DebateOpts{
				Provider:     opts.Provider,
				Registry:     opts.Registry,
				Bundle:       opts.Bundle,
				CWD:          opts.CWD,
				SessionStore: opts.SessionStore,
				SessionID:    opts.SessionID,
				Model:        opts.Model,
				Emit:         opts.Emit,
				AuditLogger:  opts.AuditLogger,
				Prompt:       opts.InitialPrompt,
			})
			if err != nil {
				return result, fmt.Errorf("ideation pipeline: %w", err)
			}

			specPath = debateResult.SpecPath
			if specPath == "" {
				specPath = findLatestSpec(opts.CWD)
			}

			result.SpecHistoryID = debateResult.PlannerHistoryID

			o.emitPhaseComplete(opts, "ideate", fmt.Sprintf("spec: %s", specPath))

		default:
			// Single-agent spec creator (original behavior)
			o.emitPhaseStart(opts, "spec")

			specResult, err := o.runSpecCreator(ctx, opts)
			if err != nil {
				return result, fmt.Errorf("spec-creator phase: %w", err)
			}

			specPath = specResult.SpecPath
			if specPath == "" {
				specPath = findLatestSpec(opts.CWD)
			}

			result.SpecHistoryID = specResult.HistoryID

			o.emitPhaseComplete(opts, "spec", fmt.Sprintf("spec: %s", specPath))
		}
	}

	// Staleness check before coding
	staleness := CheckStaleness(opts.CWD)
	if staleness.Stale {
		switch {
		case staleness.Error != nil:
			opts.Emit(types.OutboundEvent{
				ID:        uuid.New().String(),
				SessionID: opts.SessionID,
				Type:      "staleness_error",
				Content:   staleness.Error.Error(),
				Timestamp: time.Now().UnixMilli(),
			})
			// If it's a pull failure (not just uncommitted changes warning),
			// halt before coding.
			if staleness.Behind > 0 && !staleness.Pulled {
				return result, fmt.Errorf("branch is stale and auto-pull failed: %w", staleness.Error)
			}
		case staleness.Pulled:
			opts.Emit(types.OutboundEvent{
				ID:        uuid.New().String(),
				SessionID: opts.SessionID,
				Type:      "staleness_warning",
				Content:   fmt.Sprintf("Branch was %d commits behind — auto-pulled with rebase", staleness.Behind),
				Timestamp: time.Now().UnixMilli(),
			})
		}
	}

	// Phase 2: Coder
	o.emitPhaseHandoff(opts, "spec", "code")
	o.emitPhaseStart(opts, "code")

	log.Printf("[orchestrator:%s] spec phase complete (specHistoryID=%s, specPath=%s)", opts.SessionID, result.SpecHistoryID, specPath)

	var coderHistoryID string
	coderHistoryID, err := o.runCoder(ctx, opts, specPath)
	if err != nil {
		return result, fmt.Errorf("coder phase: %w", err)
	}

	o.emitPhaseComplete(opts, "code", "implementation complete")

	// Phase 3: Review → Fix loop
	var prevReviewSHA string
	for cycle := 0; cycle < o.maxReviewCycles; cycle++ {
		o.emitPhaseHandoff(opts, "code", "review")
		o.emitPhaseStart(opts, "review")

		// Determine the diff for this review cycle.
		var diff string
		incremental := false
		if cycle == 0 {
			// First cycle: full branch diff.
			d, err := review.GetDiff(opts.CWD, "")
			if err != nil {
				log.Printf("[orchestrator:%s] get diff error: %v", opts.SessionID, err)
				break
			}
			diff = d
		} else {
			// Subsequent cycles: incremental diff since last review.
			currentSHA, err := review.GetHeadSHA(opts.CWD)
			if err != nil {
				log.Printf("[orchestrator:%s] get HEAD SHA error: %v", opts.SessionID, err)
				break
			}
			if currentSHA == prevReviewSHA {
				opts.Emit(types.OutboundEvent{
					ID:        uuid.New().String(),
					SessionID: opts.SessionID,
					Type:      "review_summary",
					Content:   "No new changes to review — coder made no commits.",
					Timestamp: time.Now().Unix(),
				})
				break
			}
			d, err := review.GetIncrementalDiff(opts.CWD, prevReviewSHA)
			if err != nil {
				log.Printf("[orchestrator:%s] incremental diff error: %v", opts.SessionID, err)
				break
			}
			if strings.TrimSpace(d) == "" {
				opts.Emit(types.OutboundEvent{
					ID:        uuid.New().String(),
					SessionID: opts.SessionID,
					Type:      "review_summary",
					Content:   "No new changes to review.",
					Timestamp: time.Now().Unix(),
				})
				break
			}
			diff = d
			incremental = true
		}

		// Capture HEAD before running the review.
		sha, err := review.GetHeadSHA(opts.CWD)
		if err != nil {
			log.Printf("[orchestrator:%s] get HEAD SHA error: %v", opts.SessionID, err)
			break
		}
		prevReviewSHA = sha

		reviewResult, err := o.runReviewerWithDiff(ctx, opts, specPath, diff, incremental)
		if err != nil {
			log.Printf("[orchestrator:%s] reviewer error (continuing): %v", opts.SessionID, err)
			break
		}

		o.emitPhaseComplete(opts, "review", fmt.Sprintf("%d findings", len(reviewResult.Consolidated)))

		// Use consolidated findings for severity checks and coder formatting.
		if !review.HasConsolidatedHighSeverity(reviewResult.Consolidated) {
			break
		}

		// Feed findings back to coder
		if cycle+1 >= o.maxReviewCycles {
			if review.HasConsolidatedCritical(reviewResult.Consolidated) {
				// Critical findings remain — reset counter for another pass.
				cycle = -1 // will become 0 after loop increment
				log.Printf("[orchestrator:%s] critical findings remain after %d cycles, resetting counter", opts.SessionID, o.maxReviewCycles)
			} else {
				opts.Emit(types.OutboundEvent{
					ID:        uuid.New().String(),
					SessionID: opts.SessionID,
					Type:      "warning",
					Content:   fmt.Sprintf("Max review cycles (%d) reached — completing with remaining findings", o.maxReviewCycles),
					Timestamp: time.Now().Unix(),
				})
				break
			}
		}

		o.emitPhaseHandoff(opts, "review", "code")
		o.emitPhaseStart(opts, "code")

		fixMsg := formatConsolidatedFindingsForCoder(reviewResult.Consolidated)
		coderHistoryID, err = o.runCoderResume(ctx, opts, coderHistoryID, fixMsg)
		if err != nil {
			return result, fmt.Errorf("coder fix phase: %w", err)
		}

		o.emitPhaseComplete(opts, "code", "fixes applied")
	}

	// Review → Fix loop complete. Squash branch commits before PR creation.
	squashResult := tools.SquashBranchCommits(ctx, opts.CWD)
	if squashResult.Squashed {
		o.emitPhaseEvent(opts, "squash",
			fmt.Sprintf("squashed %d commits into 1", squashResult.CommitCount))
	}
	if squashResult.Error != nil {
		log.Printf("[orchestrator:%s] squash warning: %v", opts.SessionID, squashResult.Error)
	}

	result.CoderHistoryID = coderHistoryID
	return result, nil
}

// runMultiPhasePipeline runs the large-task multi-phase path (#224): fetch the
// parent's sub-issues (decomposing + creating them if none exist yet), then run
// each as its own worktree/branch/PR via RunMultiPhase.
//
//	┌────────────────────────┐
//	│ fetch sub-issues       │
//	└───────────┬────────────┘
//	    exist?  │  none
//	       ┌────┴─────┐
//	       │          ▼
//	       │   ┌──────────────┐
//	       │   │ Decompose    │
//	       │   │ CreateSubIss.│
//	       │   │ re-fetch     │
//	       │   └──────┬───────┘
//	       ▼          ▼
//	┌────────────────────────┐
//	│ RunMultiPhase(phases)  │
//	└────────────────────────┘
func (o *Orchestrator) runMultiPhasePipeline(ctx context.Context, opts OrchestratorOpts) (OrchestratorResult, error) {
	result := OrchestratorResult{Intent: IntentTask}
	if opts.SpawnPhaseAgentFn == nil {
		return result, fmt.Errorf("multi-phase: SpawnPhaseAgentFn is required")
	}

	subIssuesFn := opts.SubIssuesFn
	if subIssuesFn == nil {
		subIssuesFn = ghSubIssues
	}
	createSubIssuesFn := opts.CreateSubIssuesFn
	if createSubIssuesFn == nil {
		createSubIssuesFn = CreateSubIssues
	}

	repoRoot := opts.RepoRoot
	if repoRoot == "" {
		repoRoot = opts.CWD
	}
	worktreeBase := opts.WorktreeBase
	if worktreeBase == "" {
		worktreeBase = "/tmp/forge/worktrees"
	}

	o.emitPhaseStart(opts, "multi-phase")

	phases, err := subIssuesFn(ctx, opts.CWD, opts.IssueNumber)
	if err != nil {
		log.Printf("[orchestrator:%s] multi-phase: fetch sub-issues failed (parent #%d, cwd=%s): %v",
			opts.SessionID, opts.IssueNumber, opts.CWD, err)
		return result, fmt.Errorf("multi-phase: fetch sub-issues (parent #%d): %w", opts.IssueNumber, err)
	}

	// No sub-issues yet — decompose the parent and create them.
	if len(phases) == 0 {
		log.Printf("[orchestrator:%s] multi-phase: no sub-issues, decomposing parent #%d", opts.SessionID, opts.IssueNumber)
		tasks, derr := Decompose(ctx, opts.Provider, opts.IssueBody)
		if derr != nil {
			// Decompose failure falls back to the normal ideate pipeline so
			// the session still makes progress on a single branch. Surface the
			// downgrade to the user so it isn't a silent behavior change, and
			// return the fallback's OrchestratorResult so session continuity
			// (CoderHistoryID etc.) survives the fallback.
			log.Printf("[orchestrator:%s] multi-phase: decompose failed (parent #%d), falling back to ideate: %v",
				opts.SessionID, opts.IssueNumber, derr)
			o.emitWarning(opts, fmt.Sprintf("Could not decompose issue into sub-tasks (%v) — falling back to single-pipeline implementation.", derr))
			return o.runSWEPipelineNoMultiPhase(ctx, opts)
		}
		// CreateSubIssues may partially create sub-issues before failing (no
		// rollback). Halt the task rather than running an incomplete plan, but
		// warn that GitHub may now hold orphaned sub-issues a re-run will reuse.
		// The empty repo is intentional: per the decompose contract gh omits
		// the --repo flag and resolves the repo from opts.CWD (the worktree).
		if _, cerr := createSubIssuesFn(ctx, opts.IssueNumber, "", tasks); cerr != nil {
			log.Printf("[orchestrator:%s] multi-phase: create sub-issues failed (parent #%d); GitHub may hold partially-created sub-issues: %v",
				opts.SessionID, opts.IssueNumber, cerr)
			o.emitWarning(opts, fmt.Sprintf("Sub-issue creation failed partway (%v); some sub-issues may already exist on parent issue #%d. Review the parent issue on GitHub and close any partially-created sub-issues before re-running, or pass --no-plan to implement #%d as a single pipeline.", cerr, opts.IssueNumber, opts.IssueNumber))
			return result, fmt.Errorf("multi-phase: create sub-issues: %w", cerr)
		}
		phases, err = subIssuesFn(ctx, opts.CWD, opts.IssueNumber)
		if err != nil {
			log.Printf("[orchestrator:%s] multi-phase: re-fetch sub-issues failed (parent #%d): %v",
				opts.SessionID, opts.IssueNumber, err)
			return result, fmt.Errorf("multi-phase: re-fetch sub-issues (parent #%d): %w", opts.IssueNumber, err)
		}
	}

	if len(phases) == 0 {
		return result, fmt.Errorf("multi-phase: no sub-issues to run after decomposition")
	}

	return result, o.runMultiPhaseFn()(ctx, MultiPhaseOpts{
		Provider:     opts.Provider,
		RepoRoot:     repoRoot,
		BaseBranch:   DetectDefaultBranchSafe(repoRoot),
		ParentSlug:   SlugifyTitle(opts.IssueTitle, maxParentSlugLen),
		WorktreeBase: worktreeBase,
		Phases:       phases,
		Emit:         opts.Emit,
		SpawnAgent:   opts.SpawnPhaseAgentFn,
	})
}

// runMultiPhaseFn returns the orchestrator's multi-phase coordinator, defaulting
// to RunMultiPhase when unset (tests override it to avoid git/gh).
func (o *Orchestrator) runMultiPhaseFn() func(ctx context.Context, opts MultiPhaseOpts) error {
	if o.runMultiPhase != nil {
		return o.runMultiPhase
	}
	return RunMultiPhase
}

// runSWEPipelineNoMultiPhase runs the normal large-task ideate pipeline,
// bypassing multi-phase routing. Used as the fallback when decomposition fails.
func (o *Orchestrator) runSWEPipelineNoMultiPhase(ctx context.Context, opts OrchestratorOpts) (OrchestratorResult, error) {
	opts.MultiPhase = false
	return o.runSWEPipeline(ctx, opts, "", Classification{Intent: IntentTask, Size: TaskSizeLarge})
}

// ghSubIssues is the default SubIssuesFunc: it queries the GitHub sub-issues API
// for the parent and maps the result into SubIssue values in position order.
func ghSubIssues(ctx context.Context, cwd string, parentNumber int) ([]SubIssue, error) {
	if !tools.GHAvailable() {
		return nil, fmt.Errorf("gh CLI not installed (https://cli.github.com/)")
	}
	out, err := tools.GHOutputCtx(ctx, cwd,
		"api", fmt.Sprintf("repos/{owner}/{repo}/issues/%d/sub_issues", parentNumber))
	if err != nil {
		return nil, fmt.Errorf("gh api sub_issues: %w", err)
	}
	return parseGHSubIssues([]byte(out))
}

// parseGHSubIssues unmarshals the GitHub sub-issues API response into SubIssue
// values, preserving position order. Empty input returns (nil, nil).
func parseGHSubIssues(raw []byte) ([]SubIssue, error) {
	var items []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("parsing sub-issues: %w", err)
	}
	if len(items) == 0 {
		return nil, nil
	}
	subs := make([]SubIssue, len(items))
	for i, it := range items {
		subs[i] = SubIssue{Number: it.Number, Title: it.Title, Body: it.Body}
	}
	return subs, nil
}

// RunSinglePhase runs a single phase in isolation.
func RunSinglePhase(ctx context.Context, opts OrchestratorOpts, phase Phase) error {
	registry := opts.Registry
	if len(phase.AllowedTools) > 0 || len(phase.DisallowedTools) > 0 {
		registry = opts.Registry.Filtered(phase.AllowedTools, phase.DisallowedTools)
	}

	model := opts.Model
	if phase.Model != "" {
		model = phase.Model
	}

	// Inject phase-specific prompt into the context bundle.
	bundle := InjectPhasePrompt(opts.Bundle, phase.Name)

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
	return l.Send(ctx, opts.InitialPrompt, opts.Emit)
}

func (o *Orchestrator) runSpecCreator(ctx context.Context, opts OrchestratorOpts) (Result, error) {
	phase := SpecCreator()
	registry := opts.Registry.Filtered(phase.AllowedTools, phase.DisallowedTools)
	bundle := InjectPhasePrompt(opts.Bundle, phase.Name)

	model := opts.Model
	if phase.Model != "" {
		model = phase.Model
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

	var err error
	switch {
	case opts.TransitionHistoryID != "":
		// Resume from Q&A/investigate history so the spec-creator has full context.
		err = l.Resume(ctx, opts.TransitionHistoryID, opts.InitialPrompt, opts.Emit)
		if err != nil {
			// If Resume fails (e.g. missing session file), fall back to Send.
			log.Printf("[orchestrator:%s] spec-creator Resume failed (historyID=%s), falling back to Send: %v",
				opts.SessionID, opts.TransitionHistoryID, err)
			l = loop.New(loopOpts)
			err = l.Send(ctx, opts.InitialPrompt, opts.Emit)
		}
	default:
		err = l.Send(ctx, opts.InitialPrompt, opts.Emit)
	}

	if err != nil {
		log.Printf("[orchestrator:%s] spec-creator phase failed (historyID=%s, promptLen=%d): %v",
			opts.SessionID, l.HistoryID(), len(opts.InitialPrompt), err)
		return Result{Phase: "spec", HistoryID: l.HistoryID()}, err
	}

	// Try to find the spec that was just written.
	specPath := findLatestSpec(opts.CWD)
	return Result{Phase: "spec", SpecPath: specPath, HistoryID: l.HistoryID()}, nil
}

// runCoder creates a new conversation loop for the coder phase and returns its historyID.
func (o *Orchestrator) runCoder(ctx context.Context, opts OrchestratorOpts, specPath string) (string, error) {
	prompt := buildCoderPrompt(specPath)

	phase := Coder()
	bundle := InjectPhasePrompt(opts.Bundle, phase.Name)

	model := opts.Model
	if phase.Model != "" {
		model = phase.Model
	}

	loopOpts := loop.Options{
		Provider:     opts.Provider,
		Tools:        opts.Registry, // coder gets all tools
		Context:      bundle,
		CWD:          opts.CWD,
		SessionStore: opts.SessionStore,
		SessionID:    opts.SessionID,
		Model:        model,
		MaxTurns:     phase.MaxTurns,
		AuditLogger:  opts.AuditLogger,
	}

	l := loop.New(loopOpts)
	if err := l.Send(ctx, prompt, opts.Emit); err != nil {
		log.Printf("[orchestrator:%s] coder phase Send failed (historyID=%s, promptLen=%d): %v",
			opts.SessionID, l.HistoryID(), len(prompt), err)
		return "", err
	}
	return l.HistoryID(), nil
}

// runCoderDirect runs the coder phase directly with the user's prompt,
// bypassing spec creation and review. Used for small tasks.
func (o *Orchestrator) runCoderDirect(ctx context.Context, opts OrchestratorOpts) (string, error) {
	o.emitPhaseStart(opts, "code")

	phase := Coder()
	bundle := InjectPhasePrompt(opts.Bundle, phase.Name)

	model := opts.Model
	if phase.Model != "" {
		model = phase.Model
	}

	loopOpts := loop.Options{
		Provider:     opts.Provider,
		Tools:        opts.Registry, // coder gets all tools
		Context:      bundle,
		CWD:          opts.CWD,
		SessionStore: opts.SessionStore,
		SessionID:    opts.SessionID,
		Model:        model,
		MaxTurns:     phase.MaxTurns,
		AuditLogger:  opts.AuditLogger,
	}

	l := loop.New(loopOpts)

	var err error
	switch {
	case opts.TransitionHistoryID != "":
		// Resume from Q&A/investigate history so the coder has full context.
		err = l.Resume(ctx, opts.TransitionHistoryID, opts.InitialPrompt, opts.Emit)
		if err != nil {
			log.Printf("[orchestrator:%s] coder direct Resume failed (historyID=%s), falling back to Send: %v",
				opts.SessionID, opts.TransitionHistoryID, err)
			l = loop.New(loopOpts)
			err = l.Send(ctx, opts.InitialPrompt, opts.Emit)
		}
	default:
		err = l.Send(ctx, opts.InitialPrompt, opts.Emit)
	}

	if err != nil {
		log.Printf("[orchestrator:%s] coder direct failed (historyID=%s, promptLen=%d): %v",
			opts.SessionID, l.HistoryID(), len(opts.InitialPrompt), err)
		return l.HistoryID(), err
	}

	o.emitPhaseComplete(opts, "code", "implementation complete")
	return l.HistoryID(), nil
}

// runCoderResume resumes an existing coder conversation with a new message.
func (o *Orchestrator) runCoderResume(ctx context.Context, opts OrchestratorOpts, historyID, message string) (string, error) {
	phase := Coder()
	bundle := InjectPhasePrompt(opts.Bundle, phase.Name)

	model := opts.Model
	if phase.Model != "" {
		model = phase.Model
	}

	loopOpts := loop.Options{
		Provider:     opts.Provider,
		Tools:        opts.Registry, // coder gets all tools
		Context:      bundle,
		CWD:          opts.CWD,
		SessionStore: opts.SessionStore,
		SessionID:    opts.SessionID,
		Model:        model,
		MaxTurns:     phase.MaxTurns,
		AuditLogger:  opts.AuditLogger,
	}

	l := loop.New(loopOpts)
	if err := l.Resume(ctx, historyID, message, opts.Emit); err != nil {
		log.Printf("[orchestrator:%s] coder resume failed (historyID=%s, messageLen=%d): %v",
			opts.SessionID, historyID, len(message), err)
		// Return original historyID — the caller can retry with the same session.
		return historyID, err
	}
	return l.HistoryID(), nil
}

func (o *Orchestrator) runReviewer(ctx context.Context, opts OrchestratorOpts, specPath string) (Result, error) {
	// Full branch diff for manual /review — not incremental.
	diff, err := review.GetDiff(opts.CWD, "")
	if err != nil {
		return Result{Phase: "review"}, fmt.Errorf("get diff: %w", err)
	}
	return o.runReviewerWithDiff(ctx, opts, specPath, diff, false)
}

func (o *Orchestrator) runReviewerWithDiff(ctx context.Context, opts OrchestratorOpts, specPath string, diff string, incremental bool) (Result, error) {
	result := Result{Phase: "review"}

	// Collect available providers (canonical names shared with worker.runReview).
	providers := CollectReviewProviders()

	if len(providers) == 0 {
		opts.Emit(types.OutboundEvent{
			ID:        uuid.New().String(),
			SessionID: opts.SessionID,
			Type:      "review_error",
			Content:   "No providers available for review. Set ANTHROPIC_API_KEY and/or OPENAI_API_KEY.",
			Timestamp: time.Now().Unix(),
		})
		return result, nil
	}

	if diff == "" {
		opts.Emit(types.OutboundEvent{
			ID:        uuid.New().String(),
			SessionID: opts.SessionID,
			Type:      "review_summary",
			Content:   "No changes to review.",
			Timestamp: time.Now().Unix(),
		})
		return result, nil
	}

	result.Diff = diff

	// Pick reviewers.
	var reviewers []review.Reviewer
	hasActiveSpecs := false
	for _, spec := range opts.Bundle.Specs {
		if spec.Status == "active" || spec.Status == "draft" {
			hasActiveSpecs = true
			break
		}
	}

	switch hasActiveSpecs {
	case true:
		reviewers = review.DefaultReviewersWithSpec()
	default:
		reviewers = review.DefaultReviewers()
	}

	orch := review.NewOrchestrator(providers, reviewers)
	req := review.ReviewRequest{
		Diff:        diff,
		Specs:       opts.Bundle.Specs,
		Context:     opts.Bundle,
		BaseBranch:  "",
		CWD:         opts.CWD,
		Incremental: incremental,
	}

	cr := orch.Run(ctx, req, opts.Emit)

	// Flatten all raw findings into the result.
	for _, r := range cr.Raw {
		result.Findings = append(result.Findings, r.Findings...)
	}
	result.Consolidated = cr.Consolidated

	return result, nil
}

// buildCoderPrompt constructs the initial prompt for the coder phase.
func buildCoderPrompt(specPath string) string {
	if specPath == "" {
		return "Implement the changes discussed. Follow the project's coding standards and test everything."
	}

	specContent, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Sprintf(
			"Implement the spec at %s. Read the spec file first, then implement it. "+
				"Follow the spec's Behavior, Constraints, and Interfaces sections precisely. "+
				"When done, reconcile the spec with any corrections or discoveries.",
			specPath,
		)
	}

	return fmt.Sprintf(
		"Implement the following spec. The spec is the source of truth "+
			"— follow its Behavior, Constraints, and Interfaces sections precisely.\n\n"+
			"Spec file: %s\n\n%s\n\n"+
			"When done, reconcile this spec file with any corrections or "+
			"discoveries made during implementation.",
		specPath, string(specContent),
	)
}

// formatConsolidatedFindingsForCoder converts consolidated review findings into
// a prompt for the coder. Only critical and warning findings are included.
func formatConsolidatedFindingsForCoder(findings []review.ConsolidatedFinding) string {
	return review.FormatConsolidatedForCoder(findings)
}

// findLatestSpec finds the most recently modified spec in .forge/specs/.
func findLatestSpec(cwd string) string {
	specsDir := filepath.Join(cwd, ".forge", "specs")
	entries, err := os.ReadDir(specsDir)
	if err != nil {
		return ""
	}

	var latest string
	var latestTime time.Time

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latestTime) {
			latestTime = info.ModTime()
			latest = filepath.Join(specsDir, entry.Name())
		}
	}

	return latest
}

// InjectPhasePrompt adds the phase-specific prompt to the context bundle
// as an AgentsMD entry so it gets included in the system prompt.
func InjectPhasePrompt(bundle types.ContextBundle, phaseName string) types.ContextBundle {
	phasePrompt := PromptForPhase(phaseName)
	if phasePrompt == "" {
		return bundle
	}

	// Deep copy AgentsMD to avoid mutating the original.
	newAgentsMD := make([]types.AgentsMDEntry, len(bundle.AgentsMD))
	copy(newAgentsMD, bundle.AgentsMD)

	newAgentsMD = append(newAgentsMD, types.AgentsMDEntry{
		Path:    fmt.Sprintf("phase:%s", phaseName),
		Content: phasePrompt,
		Level:   "phase",
	})

	// Return a copy with the new entry.
	return types.ContextBundle{
		AgentsMD:          newAgentsMD,
		Rules:             bundle.Rules,
		SkillDescriptions: bundle.SkillDescriptions,
		AgentDefinitions:  bundle.AgentDefinitions,
		Specs:             bundle.Specs,
		Settings:          bundle.Settings,
	}
}

func (o *Orchestrator) emitPhaseStart(opts OrchestratorOpts, phase string) {
	opts.Emit(types.OutboundEvent{
		ID:        uuid.New().String(),
		SessionID: opts.SessionID,
		Type:      "phase_start",
		Content:   phase,
		Timestamp: time.Now().Unix(),
	})
}

func (o *Orchestrator) emitPhaseComplete(opts OrchestratorOpts, phase, summary string) {
	opts.Emit(types.OutboundEvent{
		ID:        uuid.New().String(),
		SessionID: opts.SessionID,
		Type:      "phase_complete",
		Content:   fmt.Sprintf("%s: %s", phase, summary),
		Timestamp: time.Now().Unix(),
	})
}

func (o *Orchestrator) emitPhaseHandoff(opts OrchestratorOpts, from, to string) {
	opts.Emit(types.OutboundEvent{
		ID:        uuid.New().String(),
		SessionID: opts.SessionID,
		Type:      "phase_handoff",
		Content:   fmt.Sprintf("%s → %s", from, to),
		Timestamp: time.Now().Unix(),
	})
}

func (o *Orchestrator) emitPhaseEvent(opts OrchestratorOpts, eventType, content string) {
	opts.Emit(types.OutboundEvent{
		ID:        uuid.New().String(),
		SessionID: opts.SessionID,
		Type:      "phase_event",
		Content:   fmt.Sprintf("%s: %s", eventType, content),
		Timestamp: time.Now().Unix(),
	})
}

// emitWarning emits a user-facing warning event. It is nil-safe: when opts.Emit
// is unset (some tests / call paths) it is a no-op rather than panicking.
func (o *Orchestrator) emitWarning(opts OrchestratorOpts, content string) {
	if opts.Emit == nil {
		return
	}
	opts.Emit(types.OutboundEvent{
		ID:        uuid.New().String(),
		SessionID: opts.SessionID,
		Type:      "warning",
		Content:   content,
		Timestamp: time.Now().UnixMilli(),
	})
}

// RunReviewOnly runs the review phase in isolation.
func RunReviewOnly(ctx context.Context, opts OrchestratorOpts) error {
	o := NewSWEOrchestrator()
	result, err := o.runReviewer(ctx, opts, opts.SpecPath)
	if err != nil {
		return err
	}
	_ = result
	return nil
}

// shouldCreatePR checks if we're in a state where PR creation makes sense.
// Returns (true, "") if yes, or (false, reason) if not.
func shouldCreatePR(cwd string) (bool, string) {
	// Must be a git repo.
	if err := tools.RunGitCmd(cwd, "rev-parse", "--git-dir"); err != nil {
		return false, "not a git repository"
	}

	// Must be on a feature branch.
	branch, err := tools.GitOutput(cwd, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return false, fmt.Sprintf("cannot determine branch: %v", err)
	}
	if branch == "main" || branch == "master" {
		return false, fmt.Sprintf("on %s branch", branch)
	}

	// Must have changes relative to base.
	base := detectDefaultBranchSafe(cwd)
	diffStat, err := tools.GitOutput(cwd, "diff", "--stat", "origin/"+base+"...HEAD")
	if err != nil {
		return false, fmt.Sprintf("cannot diff against origin/%s: %v", base, err)
	}
	if diffStat == "" {
		return false, "no changes relative to base"
	}

	return true, ""
}

// detectDefaultBranchSafe returns the default branch name without failing.
func detectDefaultBranchSafe(cwd string) string {
	for _, candidate := range []string{"main", "master"} {
		if err := tools.RunGitCmd(cwd, "rev-parse", "--verify", "origin/"+candidate); err == nil {
			return candidate
		}
	}
	return "main"
}

// DetectDefaultBranchSafe is the exported wrapper around detectDefaultBranchSafe.
// It returns the repo's base branch ("main" or "master") by checking which
// origin ref exists, matching the base used by EnsurePR's preconditions.
func DetectDefaultBranchSafe(cwd string) string {
	return detectDefaultBranchSafe(cwd)
}

// resolveTaskPipeline determines the pipeline path based on hint and task size.
// Returns (skipSpec, useIdeation).
//
//	hint "ideate" → always ideate (skipSpec=false, useIdeation=true)
//	hint "spec"   → always single-agent spec creator (skipSpec=false, useIdeation=false)
//	hint "code"   → skip spec entirely (skipSpec=true, useIdeation=false)
//	otherwise, size decides:
//	  small    → skip spec (skipSpec=true, useIdeation=false)
//	  large    → ideate (skipSpec=false, useIdeation=true)
//	  standard → normal pipeline (skipSpec=false, useIdeation=false)
func resolveTaskPipeline(hint string, size TaskSize) (skipSpec bool, useIdeation bool) {
	switch hint {
	case "ideate":
		return false, true
	case "spec":
		return false, false
	case "code":
		return true, false
	}
	switch size {
	case TaskSizeSmall:
		return true, false
	case TaskSizeLarge:
		return false, true
	default:
		return false, false
	}
}
