package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// ghIssue is the subset of `gh issue view --json` we care about.
type ghIssue struct {
	Title    string      `json:"title"`
	Body     string      `json:"body"`
	URL      string      `json:"url"`
	Comments []ghComment `json:"comments"`
}

type ghComment struct {
	Author    ghAuthor `json:"author"`
	Body      string   `json:"body"`
	CreatedAt string   `json:"createdAt"`
}

type ghAuthor struct {
	Login string `json:"login"`
}

// fetchGitHubIssue resolves a GitHub issue reference and returns the formatted
// prompt text and the issue title (for session naming). cwd is used to resolve
// relative issue numbers against the current repo.
func fetchGitHubIssue(ref string, cwd string) (prompt string, title string, err error) {
	if _, lookErr := exec.LookPath("gh"); lookErr != nil {
		return "", "", fmt.Errorf("--issue requires the GitHub CLI (gh) — install from https://cli.github.com")
	}

	normalized := normalizeIssueRef(ref)

	cmd := exec.Command("gh", "issue", "view", normalized, "--json", "title,body,url,comments")
	cmd.Dir = cwd

	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", "", fmt.Errorf("%s", msg)
	}

	var issue ghIssue
	if err := json.Unmarshal(out, &issue); err != nil {
		return "", "", fmt.Errorf("parsing gh output: %w", err)
	}

	return formatIssuePrompt(issue), issue.Title, nil
}

// normalizeIssueRef strips leading `#` and whitespace from an issue reference.
// Full URLs are passed through unchanged.
func normalizeIssueRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "#")
	return ref
}

// formatIssuePrompt builds the initial prompt text from a fetched GitHub issue.
func formatIssuePrompt(issue ghIssue) string {
	var b strings.Builder

	b.WriteString("Implement the following GitHub issue.\n\n")
	b.WriteString("Issue: ")
	b.WriteString(issue.URL)
	b.WriteString("\n\n# ")
	b.WriteString(issue.Title)

	if strings.TrimSpace(issue.Body) != "" {
		b.WriteString("\n\n")
		b.WriteString(issue.Body)
	}

	if len(issue.Comments) > 0 {
		b.WriteString("\n\n## Comments")
		for _, c := range issue.Comments {
			b.WriteString("\n\n**@")
			b.WriteString(c.Author.Login)
			b.WriteString("** (")
			b.WriteString(c.CreatedAt)
			b.WriteString("):\n")
			b.WriteString(c.Body)
		}
	}

	return b.String()
}
