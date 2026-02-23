package tracker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileTracker_GetIssue_WithFrontmatter(t *testing.T) {
	dir := t.TempDir()
	content := `---
title: My Feature
status: open
labels: [enhancement, tracker]
---

# My Feature

This is the feature description.

## Acceptance Criteria

- It works
`
	path := filepath.Join(dir, "feature.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tracker := NewFileTracker(dir)
	issue, err := tracker.GetIssue(context.Background(), "feature.md")
	if err != nil {
		t.Fatalf("GetIssue unexpected error: %v", err)
	}

	if issue.Title != "My Feature" {
		t.Errorf("Title = %q, want %q", issue.Title, "My Feature")
	}
	if issue.Status != "open" {
		t.Errorf("Status = %q, want %q", issue.Status, "open")
	}
	if len(issue.Labels) != 2 || issue.Labels[0] != "enhancement" || issue.Labels[1] != "tracker" {
		t.Errorf("Labels = %v, want [enhancement tracker]", issue.Labels)
	}
	if issue.Tracker != "file" {
		t.Errorf("Tracker = %q, want %q", issue.Tracker, "file")
	}
	if issue.ID != "feature.md" {
		t.Errorf("ID = %q, want %q", issue.ID, "feature.md")
	}
	if !strings.Contains(issue.Body, "Acceptance Criteria") {
		t.Errorf("Body should contain 'Acceptance Criteria', got: %q", issue.Body)
	}
}

func TestFileTracker_GetIssue_WithoutFrontmatter(t *testing.T) {
	dir := t.TempDir()
	content := `# Simple Issue

Just a plain markdown file without frontmatter.
`
	path := filepath.Join(dir, "simple.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tracker := NewFileTracker(dir)
	issue, err := tracker.GetIssue(context.Background(), "simple.md")
	if err != nil {
		t.Fatalf("GetIssue unexpected error: %v", err)
	}

	if issue.Title != "Simple Issue" {
		t.Errorf("Title = %q, want %q", issue.Title, "Simple Issue")
	}
	if issue.Body != content {
		t.Errorf("Body = %q, want %q", issue.Body, content)
	}
	if issue.Status != "" {
		t.Errorf("Status = %q, want empty", issue.Status)
	}
}

func TestFileTracker_GetIssue_NotFound(t *testing.T) {
	dir := t.TempDir()
	tracker := NewFileTracker(dir)
	_, err := tracker.GetIssue(context.Background(), "nonexistent.md")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "get issue nonexistent.md") {
		t.Errorf("error should contain ref, got: %v", err)
	}
}

func TestFileTracker_GetIssue_AbsolutePath(t *testing.T) {
	dir := t.TempDir()
	content := "# Absolute Path Issue\n\nBody here.\n"
	path := filepath.Join(dir, "abs.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tracker := NewFileTracker(dir)
	issue, err := tracker.GetIssue(context.Background(), path)
	if err != nil {
		t.Fatalf("GetIssue with absolute path unexpected error: %v", err)
	}
	if issue.Title != "Absolute Path Issue" {
		t.Errorf("Title = %q, want %q", issue.Title, "Absolute Path Issue")
	}
}

func TestFileTracker_CreateIssue(t *testing.T) {
	dir := t.TempDir()
	tracker := NewFileTracker(dir)

	issue, err := tracker.CreateIssue(context.Background(), &CreateIssueRequest{
		Title:  "New Feature Request",
		Body:   "We need this feature.",
		Labels: []string{"enhancement"},
	})
	if err != nil {
		t.Fatalf("CreateIssue unexpected error: %v", err)
	}

	if issue.Tracker != "file" {
		t.Errorf("Tracker = %q, want %q", issue.Tracker, "file")
	}
	if issue.Title != "New Feature Request" {
		t.Errorf("Title = %q", issue.Title)
	}
	if issue.Status != "open" {
		t.Errorf("Status = %q, want %q", issue.Status, "open")
	}

	// Verify the file was actually created.
	expectedFile := filepath.Join(dir, "new-feature-request.md")
	data, err := os.ReadFile(expectedFile)
	if err != nil {
		t.Fatalf("created file not found: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "title: New Feature Request") {
		t.Errorf("file should contain frontmatter title, got:\n%s", content)
	}
	if !strings.Contains(content, "labels: [enhancement]") {
		t.Errorf("file should contain labels, got:\n%s", content)
	}
	if !strings.Contains(content, "We need this feature.") {
		t.Errorf("file should contain body, got:\n%s", content)
	}
}

func TestFileTracker_CreateIssue_CreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "issues")
	tracker := NewFileTracker(dir)

	_, err := tracker.CreateIssue(context.Background(), &CreateIssueRequest{
		Title: "Nested Issue",
		Body:  "Body",
	})
	if err != nil {
		t.Fatalf("CreateIssue unexpected error: %v", err)
	}

	// Verify directory was created.
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatalf("expected directory to be created: %s", dir)
	}
}

func TestFileTracker_CreatePR_NotSupported(t *testing.T) {
	tracker := NewFileTracker(t.TempDir())
	_, err := tracker.CreatePR(context.Background(), &CreatePRRequest{
		Title: "PR",
		Body:  "body",
		Head:  "branch",
		Base:  "main",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrNotSupported) {
		t.Errorf("error should wrap ErrNotSupported, got: %v", err)
	}
}

func TestFileTracker_Comment_NotSupported(t *testing.T) {
	tracker := NewFileTracker(t.TempDir())
	err := tracker.Comment(context.Background(), "ref", "body")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrNotSupported) {
		t.Errorf("error should wrap ErrNotSupported, got: %v", err)
	}
}

func TestFileTracker_Link_NotSupported(t *testing.T) {
	tracker := NewFileTracker(t.TempDir())
	err := tracker.Link(context.Background(), "a", "b", RelBlocks)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrNotSupported) {
		t.Errorf("error should wrap ErrNotSupported, got: %v", err)
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Hello World", "hello-world"},
		{"My Feature Request", "my-feature-request"},
		{"  spaces  and  stuff  ", "spaces-and-stuff"},
		{"UPPERCASE", "uppercase"},
		{"special!@#chars", "special-chars"},
		{"already-slugified", "already-slugified"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := slugify(tt.input)
			if got != tt.want {
				t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseMarkdownIssue_FrontmatterOnly(t *testing.T) {
	content := `---
title: From Frontmatter
status: closed
labels: [bug, critical]
url: https://example.com/issue/1
---

Some body content.
`
	issue, err := parseMarkdownIssue(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.Title != "From Frontmatter" {
		t.Errorf("Title = %q, want %q", issue.Title, "From Frontmatter")
	}
	if issue.Status != "closed" {
		t.Errorf("Status = %q, want %q", issue.Status, "closed")
	}
	if issue.URL != "https://example.com/issue/1" {
		t.Errorf("URL = %q", issue.URL)
	}
	if len(issue.Labels) != 2 {
		t.Errorf("Labels = %v, want 2 labels", issue.Labels)
	}
}

func TestParseMarkdownIssue_HeadingFallback(t *testing.T) {
	content := `# Title from Heading

Body paragraph.
`
	issue, err := parseMarkdownIssue(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.Title != "Title from Heading" {
		t.Errorf("Title = %q, want %q", issue.Title, "Title from Heading")
	}
}

func TestParseMarkdownIssue_EmptyLabels(t *testing.T) {
	content := `---
title: No Labels
labels: []
---

Body.
`
	issue, err := parseMarkdownIssue(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.Title != "No Labels" {
		t.Errorf("Title = %q", issue.Title)
	}
	if len(issue.Labels) != 0 {
		t.Errorf("Labels = %v, want empty", issue.Labels)
	}
}
