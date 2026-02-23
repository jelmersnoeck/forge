package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
)

// GitHubTracker implements the Tracker interface by shelling out to the gh CLI.
type GitHubTracker struct {
	org  string
	repo string
}

// NewGitHubTracker creates a new GitHub tracker for the given org and repo.
func NewGitHubTracker(org, repo string) *GitHubTracker {
	return &GitHubTracker{org: org, repo: repo}
}

// repoFlag returns the --repo flag value for gh commands.
func (g *GitHubTracker) repoFlag() string {
	return fmt.Sprintf("%s/%s", g.org, g.repo)
}

// ghIssueJSON is the JSON structure returned by `gh issue view --json`.
type ghIssueJSON struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	URL    string `json:"url"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

// ghCreateIssueJSON is the JSON structure returned by `gh issue create --json`.
type ghCreateIssueJSON struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Title  string `json:"title"`
}

// ghCreatePRJSON is the JSON structure returned by `gh pr create --json`.
type ghCreatePRJSON struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// runGH executes a gh CLI command and returns its stdout.
// This is a variable so tests can override it.
var runGH = func(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh %s: %s", strings.Join(args, " "), string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("gh %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// GetIssue fetches an issue from GitHub by its issue number (as a string).
func (g *GitHubTracker) GetIssue(ctx context.Context, ref string) (*Issue, error) {
	slog.DebugContext(ctx, "fetching github issue", "ref", ref, "repo", g.repoFlag())

	out, err := runGH(ctx,
		"issue", "view", ref,
		"--json", "number,title,body,labels,state,url",
		"--repo", g.repoFlag(),
	)
	if err != nil {
		return nil, fmt.Errorf("get issue %s: %w", ref, err)
	}

	var ghIssue ghIssueJSON
	if err := json.Unmarshal(out, &ghIssue); err != nil {
		return nil, fmt.Errorf("get issue %s: parsing response: %w", ref, err)
	}

	labels := make([]string, 0, len(ghIssue.Labels))
	for _, l := range ghIssue.Labels {
		labels = append(labels, l.Name)
	}

	return &Issue{
		ID:      strconv.Itoa(ghIssue.Number),
		Tracker: "github",
		Title:   ghIssue.Title,
		Body:    ghIssue.Body,
		Labels:  labels,
		Repo:    g.repoFlag(),
		Status:  strings.ToLower(ghIssue.State),
		URL:     ghIssue.URL,
	}, nil
}

// CreateIssue creates a new issue on GitHub.
func (g *GitHubTracker) CreateIssue(ctx context.Context, req *CreateIssueRequest) (*Issue, error) {
	slog.DebugContext(ctx, "creating github issue", "title", req.Title, "repo", g.repoFlag())

	args := []string{
		"issue", "create",
		"--title", req.Title,
		"--body", req.Body,
		"--repo", g.repoFlag(),
	}
	for _, label := range req.Labels {
		args = append(args, "--label", label)
	}

	out, err := runGH(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("create issue: %w", err)
	}

	// gh issue create outputs the URL on stdout.
	url := strings.TrimSpace(string(out))

	return &Issue{
		Tracker: "github",
		Title:   req.Title,
		Body:    req.Body,
		Labels:  req.Labels,
		Repo:    g.repoFlag(),
		URL:     url,
	}, nil
}

// CreatePR creates a pull request on GitHub.
func (g *GitHubTracker) CreatePR(ctx context.Context, req *CreatePRRequest) (*PullRequest, error) {
	slog.DebugContext(ctx, "creating github PR", "title", req.Title, "head", req.Head, "base", req.Base)

	args := []string{
		"pr", "create",
		"--title", req.Title,
		"--body", req.Body,
		"--head", req.Head,
		"--base", req.Base,
		"--repo", g.repoFlag(),
	}
	if req.DraftMode {
		args = append(args, "--draft")
	}
	for _, label := range req.Labels {
		args = append(args, "--label", label)
	}

	out, err := runGH(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("create pr: %w", err)
	}

	// gh pr create outputs the URL on stdout.
	url := strings.TrimSpace(string(out))

	return &PullRequest{
		URL: url,
	}, nil
}

// Comment adds a comment to an issue on GitHub.
func (g *GitHubTracker) Comment(ctx context.Context, ref string, body string) error {
	slog.DebugContext(ctx, "commenting on github issue", "ref", ref)

	_, err := runGH(ctx,
		"issue", "comment", ref,
		"--body", body,
		"--repo", g.repoFlag(),
	)
	if err != nil {
		return fmt.Errorf("comment on %s: %w", ref, err)
	}
	return nil
}

// Link creates a dependency link between two issues using a comment.
// GitHub does not have native issue linking, so we use comments to express relationships.
func (g *GitHubTracker) Link(ctx context.Context, from string, to string, rel LinkRelation) error {
	slog.DebugContext(ctx, "linking github issues", "from", from, "to", to, "rel", rel)

	body := fmt.Sprintf("**%s** #%s", rel, to)
	_, err := runGH(ctx,
		"issue", "comment", from,
		"--body", body,
		"--repo", g.repoFlag(),
	)
	if err != nil {
		return fmt.Errorf("link %s -> %s: %w", from, to, err)
	}
	return nil
}
