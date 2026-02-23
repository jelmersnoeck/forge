package gitutil

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// initTestRepo creates a git repo in a temp directory with an initial commit.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	g := New(dir)
	ctx := context.Background()

	if _, err := g.run(ctx, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	// Configure user for commits.
	if _, err := g.run(ctx, "config", "user.email", "test@forge.dev"); err != nil {
		t.Fatalf("git config email: %v", err)
	}
	if _, err := g.run(ctx, "config", "user.name", "Forge Test"); err != nil {
		t.Fatalf("git config name: %v", err)
	}

	// Create initial commit so we have a branch.
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# Test Repo\n"), 0644); err != nil {
		t.Fatalf("writing README: %v", err)
	}
	if _, err := g.run(ctx, "add", "-A"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := g.run(ctx, "commit", "-m", "initial commit"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	return dir
}

func TestCurrentBranch(t *testing.T) {
	dir := initTestRepo(t)
	g := New(dir)
	ctx := context.Background()

	branch, err := g.CurrentBranch(ctx)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	// Default branch could be "main" or "master" depending on git config.
	if branch == "" {
		t.Fatal("CurrentBranch returned empty string")
	}
}

func TestCreateBranch(t *testing.T) {
	dir := initTestRepo(t)
	g := New(dir)
	ctx := context.Background()

	err := g.CreateBranch(ctx, "forge/test-branch")
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	branch, err := g.CurrentBranch(ctx)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if branch != "forge/test-branch" {
		t.Errorf("expected branch 'forge/test-branch', got %q", branch)
	}
}

func TestCreateBranch_AlreadyExists(t *testing.T) {
	dir := initTestRepo(t)
	g := New(dir)
	ctx := context.Background()

	if err := g.CreateBranch(ctx, "feature-1"); err != nil {
		t.Fatalf("first CreateBranch: %v", err)
	}
	// Switch back to default branch, then try creating same branch.
	if _, err := g.run(ctx, "checkout", "-"); err != nil {
		t.Fatalf("checkout back: %v", err)
	}
	err := g.CreateBranch(ctx, "feature-1")
	if err == nil {
		t.Fatal("expected error creating duplicate branch, got nil")
	}
}

func TestDiff(t *testing.T) {
	dir := initTestRepo(t)
	g := New(dir)
	ctx := context.Background()

	// Get the current branch name for diffing.
	baseBranch, err := g.CurrentBranch(ctx)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}

	// Create a new branch and make changes.
	if err := g.CreateBranch(ctx, "feature"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	// Add a new file and commit it.
	newFile := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(newFile, []byte("hello\n"), 0644); err != nil {
		t.Fatalf("writing file: %v", err)
	}
	if err := g.AddAll(ctx); err != nil {
		t.Fatalf("AddAll: %v", err)
	}
	if err := g.Commit(ctx, "add new file"); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	diff, err := g.Diff(ctx, baseBranch)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff == "" {
		t.Error("expected non-empty diff")
	}
}

func TestDiffStaged(t *testing.T) {
	dir := initTestRepo(t)
	g := New(dir)
	ctx := context.Background()

	// Add a file and stage it without committing.
	newFile := filepath.Join(dir, "staged.txt")
	if err := os.WriteFile(newFile, []byte("staged content\n"), 0644); err != nil {
		t.Fatalf("writing file: %v", err)
	}
	if err := g.AddAll(ctx); err != nil {
		t.Fatalf("AddAll: %v", err)
	}

	diff, err := g.DiffStaged(ctx)
	if err != nil {
		t.Fatalf("DiffStaged: %v", err)
	}
	if diff == "" {
		t.Error("expected non-empty staged diff")
	}
}

func TestAddAllAndCommit(t *testing.T) {
	dir := initTestRepo(t)
	g := New(dir)
	ctx := context.Background()

	// Create a file.
	newFile := filepath.Join(dir, "committed.txt")
	if err := os.WriteFile(newFile, []byte("commit me\n"), 0644); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	if err := g.AddAll(ctx); err != nil {
		t.Fatalf("AddAll: %v", err)
	}
	if err := g.Commit(ctx, "test commit"); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Verify the commit exists in the log.
	output, err := g.run(ctx, "log", "--oneline", "-1")
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if output == "" {
		t.Fatal("expected commit in log")
	}
}

func TestFormatBranch(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		data    map[string]string
		want    string
	}{
		{
			name:    "standard pattern",
			pattern: "forge/{{.Tracker}}-{{.IssueID}}",
			data:    map[string]string{"Tracker": "github", "IssueID": "123"},
			want:    "forge/github-123",
		},
		{
			name:    "workstream pattern",
			pattern: "forge/ws-{{.WorkstreamID}}",
			data:    map[string]string{"WorkstreamID": "abc-def"},
			want:    "forge/ws-abc-def",
		},
		{
			name:    "no placeholders",
			pattern: "forge/static-branch",
			data:    map[string]string{"Tracker": "github"},
			want:    "forge/static-branch",
		},
		{
			name:    "missing key leaves placeholder",
			pattern: "forge/{{.Tracker}}-{{.IssueID}}",
			data:    map[string]string{"Tracker": "github"},
			want:    "forge/github-{{.IssueID}}",
		},
		{
			name:    "empty data",
			pattern: "forge/{{.Tracker}}-{{.IssueID}}",
			data:    map[string]string{},
			want:    "forge/{{.Tracker}}-{{.IssueID}}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatBranch(tt.pattern, tt.data)
			if got != tt.want {
				t.Errorf("FormatBranch(%q, %v) = %q, want %q", tt.pattern, tt.data, got, tt.want)
			}
		})
	}
}
