package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jelmersnoeck/forge/internal/principles"
	"github.com/jelmersnoeck/forge/internal/review"
)

func TestReviewCmdFlags(t *testing.T) {
	flags := reviewCmd.Flags()

	diffFlag := flags.Lookup("diff")
	if diffFlag == nil {
		t.Fatal("review command missing --diff flag")
	}

	principlesFlag := flags.Lookup("principles")
	if principlesFlag == nil {
		t.Fatal("review command missing --principles flag")
	}

	formatFlag := flags.Lookup("format")
	if formatFlag == nil {
		t.Fatal("review command missing --format flag")
	}
	if formatFlag.DefValue != "text" {
		t.Errorf("--format default = %q, want %q", formatFlag.DefValue, "text")
	}
}

func TestResolveDiff_File(t *testing.T) {
	dir := t.TempDir()
	diffPath := filepath.Join(dir, "test.diff")
	diffContent := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1 +1 @@
-old
+new
`
	if err := os.WriteFile(diffPath, []byte(diffContent), 0644); err != nil {
		t.Fatalf("writing test diff: %v", err)
	}

	got, err := resolveDiff(dir, diffPath)
	if err != nil {
		t.Fatalf("resolveDiff() error: %v", err)
	}

	if got != diffContent {
		t.Errorf("resolveDiff() = %q, want %q", got, diffContent)
	}
}

func TestResolveDiff_NonexistentFile_FallsToGit(t *testing.T) {
	dir := t.TempDir()

	// A non-existent file path that looks like a diff range should fall through
	// to git diff, which will fail because it is not a git repo. That is OK;
	// we are testing the fallthrough behavior.
	_, err := resolveDiff(dir, "HEAD~1..HEAD")
	if err == nil {
		t.Fatal("expected error for git diff in non-git directory")
	}
}

func TestOutputReviewText_NoFindings(t *testing.T) {
	result := &review.Result{
		Findings:    nil,
		HasCritical: false,
	}

	err := outputReviewText(result)
	if err != nil {
		t.Fatalf("outputReviewText() error: %v", err)
	}
}

func TestOutputReviewText_WithFindings(t *testing.T) {
	result := &review.Result{
		Findings: []review.Finding{
			{
				Severity:    principles.SeverityCritical,
				Message:     "Hardcoded secret",
				File:        "config.go",
				Line:        15,
				PrincipleID: "sec-001",
				Suggestion:  "Use environment variables",
			},
			{
				Severity: principles.SeverityWarning,
				Message:  "Missing error check",
				File:     "handler.go",
				Line:     42,
			},
		},
		HasCritical: true,
	}

	err := outputReviewText(result)
	if err != nil {
		t.Fatalf("outputReviewText() error: %v", err)
	}
}

func TestOutputSARIF(t *testing.T) {
	result := &review.Result{
		Findings: []review.Finding{
			{
				Severity:    principles.SeverityCritical,
				Message:     "Test finding",
				File:        "test.go",
				Line:        10,
				PrincipleID: "test-001",
			},
		},
		HasCritical: true,
	}

	err := outputSARIF(result)
	if err != nil {
		t.Fatalf("outputSARIF() error: %v", err)
	}
}

func TestOutputReviewResult_JSON(t *testing.T) {
	result := &review.Result{
		Findings: []review.Finding{
			{
				Severity: principles.SeverityInfo,
				Message:  "Consider refactoring",
			},
		},
		HasCritical: false,
	}

	err := outputReviewResult(result, "json")
	if err != nil {
		t.Fatalf("outputReviewResult(json) error: %v", err)
	}
}

func TestOutputReviewResult_Text(t *testing.T) {
	result := &review.Result{
		Findings:    nil,
		HasCritical: false,
	}

	err := outputReviewResult(result, "text")
	if err != nil {
		t.Fatalf("outputReviewResult(text) error: %v", err)
	}
}
