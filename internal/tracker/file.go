package tracker

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// FileTracker implements the Tracker interface using local markdown files.
// It is useful for local development and testing without requiring an
// external tracker service.
type FileTracker struct {
	// baseDir is the directory where issue files are stored.
	baseDir string
}

// NewFileTracker creates a new file-based tracker rooted at baseDir.
func NewFileTracker(baseDir string) *FileTracker {
	return &FileTracker{baseDir: baseDir}
}

// GetIssue reads a markdown file and parses it as an issue.
// The ref is treated as a file path relative to baseDir. Absolute paths and
// paths that resolve outside baseDir are rejected.
// Frontmatter between --- delimiters is parsed for metadata.
func (f *FileTracker) GetIssue(ctx context.Context, ref string) (*Issue, error) {
	path, err := f.resolvePath(ref)
	if err != nil {
		return nil, fmt.Errorf("get issue %s: %w", ref, err)
	}
	slog.DebugContext(ctx, "reading file issue", "path", path)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("get issue %s: %w", ref, err)
	}

	issue, err := parseMarkdownIssue(string(data))
	if err != nil {
		return nil, fmt.Errorf("get issue %s: %w", ref, err)
	}

	issue.ID = ref
	issue.Tracker = "file"

	return issue, nil
}

// CreateIssue creates a new markdown file representing an issue.
func (f *FileTracker) CreateIssue(ctx context.Context, req *CreateIssueRequest) (*Issue, error) {
	slog.DebugContext(ctx, "creating file issue", "title", req.Title)

	// Generate a filename from the title.
	filename := slugify(req.Title) + ".md"
	path := filepath.Join(f.baseDir, filename)

	// Build the markdown content with frontmatter.
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("title: %s\n", req.Title))
	if len(req.Labels) > 0 {
		sb.WriteString(fmt.Sprintf("labels: [%s]\n", strings.Join(req.Labels, ", ")))
	}
	sb.WriteString("status: open\n")
	sb.WriteString("---\n\n")
	sb.WriteString(fmt.Sprintf("# %s\n\n", req.Title))
	sb.WriteString(req.Body)
	sb.WriteString("\n")

	if err := os.MkdirAll(f.baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create issue: %w", err)
	}

	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		return nil, fmt.Errorf("create issue: %w", err)
	}

	return &Issue{
		ID:      filename,
		Tracker: "file",
		Title:   req.Title,
		Body:    req.Body,
		Labels:  req.Labels,
		Status:  "open",
	}, nil
}

// CreatePR is not supported by the file tracker.
func (f *FileTracker) CreatePR(_ context.Context, _ *CreatePRRequest) (*PullRequest, error) {
	return nil, fmt.Errorf("file tracker: create pr: %w", ErrNotSupported)
}

// Comment is not supported by the file tracker.
func (f *FileTracker) Comment(_ context.Context, _ string, _ string) error {
	return fmt.Errorf("file tracker: comment: %w", ErrNotSupported)
}

// Link is not supported by the file tracker.
func (f *FileTracker) Link(_ context.Context, _, _ string, _ LinkRelation) error {
	return fmt.Errorf("file tracker: link: %w", ErrNotSupported)
}

// resolvePath resolves a reference to an absolute file path within baseDir.
// It rejects absolute paths and any path that would escape baseDir via
// directory traversal.
func (f *FileTracker) resolvePath(ref string) (string, error) {
	joined := filepath.Join(f.baseDir, ref)
	cleaned := filepath.Clean(joined)
	base := filepath.Clean(f.baseDir)

	// The cleaned path must be within baseDir. We check that it either equals
	// baseDir or starts with baseDir followed by a separator to prevent
	// partial-prefix matches (e.g. /tmp/issuesEvil matching /tmp/issues).
	if cleaned != base && !strings.HasPrefix(cleaned, base+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal detected: %s resolves outside base directory", ref)
	}

	return cleaned, nil
}

// parseMarkdownIssue parses a markdown string into an Issue.
// It supports optional YAML frontmatter between --- delimiters.
func parseMarkdownIssue(content string) (*Issue, error) {
	issue := &Issue{}
	body := content

	// Parse frontmatter if present.
	if strings.HasPrefix(content, "---\n") {
		end := strings.Index(content[4:], "\n---")
		if end >= 0 {
			frontmatter := content[4 : 4+end]
			body = strings.TrimSpace(content[4+end+4:])
			parseFrontmatter(frontmatter, issue)
		}
	}

	// Extract title from first heading if not set by frontmatter.
	if issue.Title == "" {
		scanner := bufio.NewScanner(strings.NewReader(body))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "# ") {
				issue.Title = strings.TrimPrefix(line, "# ")
				break
			}
		}
	}

	issue.Body = body
	return issue, nil
}

// parseFrontmatter extracts metadata from YAML-like frontmatter.
// This is a simple key: value parser, not a full YAML parser.
func parseFrontmatter(fm string, issue *Issue) {
	scanner := bufio.NewScanner(strings.NewReader(fm))
	for scanner.Scan() {
		line := scanner.Text()
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		switch key {
		case "title":
			issue.Title = value
		case "status":
			issue.Status = value
		case "labels":
			// Parse [label1, label2] format.
			value = strings.TrimPrefix(value, "[")
			value = strings.TrimSuffix(value, "]")
			if value != "" {
				parts := strings.Split(value, ",")
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if p != "" {
						issue.Labels = append(issue.Labels, p)
					}
				}
			}
		case "url":
			issue.URL = value
		}
	}
}

// slugify converts a title to a filename-safe slug.
func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, s)
	// Collapse multiple hyphens.
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	return s
}
