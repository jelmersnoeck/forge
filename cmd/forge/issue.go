package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// ghIssue is the subset of `gh issue view --json` we care about.
type ghIssue struct {
	Title    string      `json:"title"`
	Body     string      `json:"body"`
	URL      string      `json:"url"`
	Number   int         `json:"number"`
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
// prompt text, issue title (for session naming), issue number, and issue URL.
// cwd is used to resolve relative issue numbers against the current repo.
func fetchGitHubIssue(ref string, cwd string) (prompt string, title string, issueNum int, issueURL string, err error) {
	if _, lookErr := exec.LookPath("gh"); lookErr != nil {
		return "", "", 0, "", fmt.Errorf("--issue requires the GitHub CLI (gh) — install from https://cli.github.com")
	}

	normalized := normalizeIssueRef(ref)

	cmd := exec.Command("gh", "issue", "view", normalized, "--json", "title,body,url,number,comments")
	cmd.Dir = cwd

	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", "", 0, "", fmt.Errorf("%s", msg)
	}

	var issue ghIssue
	if err := json.Unmarshal(out, &issue); err != nil {
		return "", "", 0, "", fmt.Errorf("parsing gh output: %w", err)
	}

	// Use the number from the JSON response; fall back to parsing the ref.
	num := issue.Number
	if num == 0 {
		num = extractIssueNumber(ref)
	}

	return formatIssuePrompt(issue), issue.Title, num, issue.URL, nil
}

// fetchSubIssues fetches the sub-issues attached to a parent issue, in the
// order the GitHub sub-issues API returns them (position order). cwd is used to
// resolve a relative issue reference against the current repo.
//
// Returns (nil, nil) when the parent has no sub-issues.
func fetchSubIssues(ref string, cwd string) ([]ghIssue, error) {
	if _, lookErr := exec.LookPath("gh"); lookErr != nil {
		return nil, fmt.Errorf("--issue requires the GitHub CLI (gh) — install from https://cli.github.com")
	}

	normalized := normalizeIssueRef(ref)
	// A full URL ref won't substitute into {owner}/{repo}; reduce it to the
	// bare issue number so gh resolves the placeholders against cwd's repo.
	if strings.Contains(normalized, "/") {
		if n := extractIssueNumber(ref); n > 0 {
			normalized = strconv.Itoa(n)
		}
	}

	cmd := exec.Command("gh", "api", "repos/{owner}/{repo}/issues/"+normalized+"/sub_issues")
	cmd.Dir = cwd

	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s", msg)
	}

	return parseSubIssues(out)
}

// parseSubIssues unmarshals the GitHub sub-issues API response into ghIssue
// slices, preserving the API's position order. Empty input returns (nil, nil).
func parseSubIssues(raw []byte) ([]ghIssue, error) {
	var subs []ghIssue
	if err := json.Unmarshal(raw, &subs); err != nil {
		return nil, fmt.Errorf("parsing gh sub-issues output: %w", err)
	}
	if len(subs) == 0 {
		return nil, nil
	}
	return subs, nil
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

// issueURLNumberRe matches /issues/<number> in a GitHub URL path.
var issueURLNumberRe = regexp.MustCompile(`/issues/(\d+)`)

// extractIssueNumber parses an issue number from a URL, #N shorthand, or plain
// number string. Returns 0 if the ref doesn't contain a valid positive number.
func extractIssueNumber(ref string) int {
	ref = strings.TrimSpace(ref)

	// Try URL pattern first.
	if m := issueURLNumberRe.FindStringSubmatch(ref); len(m) == 2 {
		n, _ := strconv.Atoi(m[1])
		if n > 0 {
			return n
		}
		return 0
	}

	// Strip leading # for shorthand.
	ref = strings.TrimPrefix(ref, "#")

	n, err := strconv.Atoi(ref)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}
