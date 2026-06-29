package phase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jelmersnoeck/forge/internal/types"
)

// SubTask is one independently-shippable phase decomposed from a parent issue.
// DependsOn holds zero-based indices into the decomposed slice; foundational
// phases come first.
type SubTask struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	DependsOn []int  `json:"depends_on"`
}

// maxDecomposePromptLen caps the issue body sent to the decomposer to bound
// input tokens.
const maxDecomposePromptLen = 8000

// decomposeTimeout is the per-attempt timeout for a decompose call. Larger than
// classification because the output (a JSON array of phases) is bigger.
const decomposeTimeout = 30 * time.Second

// decomposeSystemPrompt instructs the LLM to break an issue into ordered,
// independently-shippable phases.
const decomposeSystemPrompt = `You decompose a large software issue into ordered, independently-shippable sub-tasks.

Rules:
- Break the issue into the smallest set of phases that can each ship on their own (own branch, own PR).
- Order by dependency: foundational changes first. A phase may only depend on earlier phases.
- Each phase needs a concise, actionable title and a body describing the work, files, and acceptance criteria.
- depends_on lists the zero-based indices of earlier phases this phase builds on (empty if none).
- If the issue is already small enough to ship as one unit, return a single-element array.

Respond with ONLY a JSON array, no prose, no code fences:
[{"title":"...","body":"...","depends_on":[0]}]`

// Decompose runs a lightweight LLM call to break issueBody into ordered
// sub-tasks. It tries each model in types.LightweightModels in order, falling
// through on error.
//
// Returns (nil, err) for an empty/whitespace-only body (no LLM call is made).
// Returns (nil, nil) when the LLM legitimately produces an empty array.
func Decompose(ctx context.Context, provider types.LLMProvider, issueBody string) ([]SubTask, error) {
	if strings.TrimSpace(issueBody) == "" {
		return nil, fmt.Errorf("decompose: empty issue body")
	}

	body := truncateAtWordBoundary(issueBody, maxDecomposePromptLen)

	var lastErr error
	for i, model := range types.LightweightModels {
		tasks, err := decomposeWithModel(ctx, provider, model, body)
		if err == nil {
			if i > 0 {
				slog.Info("decompose: succeeded on fallback model",
					"model", model, "failed_attempts", i)
			}
			return tasks, nil
		}
		lastErr = err
		slog.Warn("decompose: model failed", "model", model, "error", err)
	}

	if lastErr == nil {
		return nil, fmt.Errorf("decompose: all models failed (no models configured)")
	}
	return nil, fmt.Errorf("decompose: all models failed: %w", lastErr)
}

// decomposeWithModel runs a single decompose attempt against a specific model.
func decomposeWithModel(ctx context.Context, provider types.LLMProvider, model, body string) ([]SubTask, error) {
	callCtx, cancel := context.WithTimeout(ctx, decomposeTimeout)
	defer cancel()

	req := types.ChatRequest{
		Model: model,
		System: []types.SystemBlock{
			{Type: "text", Text: decomposeSystemPrompt},
		},
		Messages: []types.ChatMessage{
			{
				Role: "user",
				Content: []types.ChatContentBlock{
					{Type: "text", Text: body},
				},
			},
		},
		MaxTokens: 4096,
		Stream:    true,
	}

	deltaChan, err := provider.Chat(callCtx, req)
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

	return parseDecomposition(text.String())
}

// parseDecomposition strips code fences, unmarshals the JSON array, and
// validates each task. A missing/empty title is fatal; an out-of-range
// depends_on index is dropped (logged), not fatal.
func parseDecomposition(raw string) ([]SubTask, error) {
	stripped := stripCodeFences(raw)

	var tasks []SubTask
	if err := json.Unmarshal([]byte(stripped), &tasks); err != nil {
		slog.Error("decompose: malformed LLM response",
			"reason", "parse_failed", "raw", raw, "error", err)
		return nil, fmt.Errorf("decompose: parse error: %w — raw: %q", err, raw)
	}

	if len(tasks) == 0 {
		return nil, nil
	}

	for i := range tasks {
		if strings.TrimSpace(tasks[i].Title) == "" {
			return nil, fmt.Errorf("decompose: sub-task %d has empty title", i)
		}
		tasks[i].DependsOn = sanitizeDependsOn(tasks[i].DependsOn, len(tasks), i)
	}

	return tasks, nil
}

// sanitizeDependsOn drops indices that are out of range [0, n) (logged at info).
// Returns nil when no valid dependencies remain.
func sanitizeDependsOn(deps []int, n, self int) []int {
	var valid []int
	for _, d := range deps {
		if d < 0 || d >= n {
			slog.Info("decompose: dropping out-of-range depends_on index",
				"index", d, "task", self, "total", n)
			continue
		}
		valid = append(valid, d)
	}
	return valid
}

// CreateSubIssues creates one GitHub issue per task and attaches each as a
// sub-issue of parent. Returns the created sub-issue numbers in task order.
//
// On failure it returns the numbers created so far plus the error (no rollback).
func CreateSubIssues(ctx context.Context, parent int, repo string, tasks []SubTask) ([]int, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, fmt.Errorf("sub-issue creation requires the GitHub CLI (gh) — install from https://cli.github.com")
	}

	created := make([]int, 0, len(tasks))
	for i, task := range tasks {
		num, err := createSubIssue(ctx, parent, repo, task)
		if err != nil {
			return created, fmt.Errorf("sub-task %d (%q): %w", i, task.Title, err)
		}
		created = append(created, num)
	}
	return created, nil
}

// createSubIssue creates a single issue and attaches it to the parent.
func createSubIssue(ctx context.Context, parent int, repo string, task SubTask) (int, error) {
	createArgs := []string{
		"issue", "create",
		"--title", task.Title,
		"--body", task.Body,
	}
	if repo != "" {
		createArgs = append(createArgs, "--repo", repo)
	}

	out, err := runGH(ctx, createArgs...)
	if err != nil {
		return 0, fmt.Errorf("gh issue create: %w", err)
	}

	num := extractIssueNumberFromURL(strings.TrimSpace(out))
	if num == 0 {
		return 0, fmt.Errorf("could not parse issue number from gh output: %q", out)
	}

	attachArgs := []string{
		"api", fmt.Sprintf("repos/%s/issues/%d/sub_issues", repo, parent),
		"-X", "POST",
		"-F", "sub_issue_id=" + strconv.Itoa(num),
	}
	if _, err := runGH(ctx, attachArgs...); err != nil {
		return num, fmt.Errorf("gh api sub_issues attach: %w", err)
	}

	return num, nil
}

// issueURLNumberRe matches /issues/<number> in a gh-returned issue URL.
var issueURLNumberRe = regexp.MustCompile(`/issues/(\d+)`)

// extractIssueNumberFromURL parses the issue number from a gh issue create URL.
// Returns 0 if no number is found.
func extractIssueNumberFromURL(s string) int {
	m := issueURLNumberRe.FindStringSubmatch(strings.TrimSpace(s))
	if len(m) != 2 {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// runGH runs a gh command and returns combined stdout, surfacing stderr on error.
func runGH(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return string(out), nil
}
