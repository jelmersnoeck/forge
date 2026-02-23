package engine

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/principles"
	"github.com/jelmersnoeck/forge/internal/review"
	"github.com/jelmersnoeck/forge/internal/tracker"
)

// initBuildTestRepo creates a git repo suitable for build tests.
func initBuildTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@forge.dev"},
		{"git", "config", "user.name", "Forge Test"},
	}
	for _, args := range cmds {
		cmd := exec.CommandContext(context.Background(), args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", args, err, out)
		}
	}

	// Create initial file and commit on main.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "-A"},
		{"git", "commit", "-m", "initial"},
		{"git", "branch", "-M", "main"},
	} {
		cmd := exec.CommandContext(context.Background(), args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", args, err, out)
		}
	}

	return dir
}

func TestBuild_SuccessFirstIteration(t *testing.T) {
	dir := initBuildTestRepo(t)

	codeCallCount := 0
	ma := &mockAgent{
		RunFunc: func(_ context.Context, req agent.Request) (*agent.Response, error) {
			switch req.Mode {
			case agent.ModePlan:
				return &agent.Response{Output: "Implementation plan here"}, nil
			case agent.ModeCode:
				codeCallCount++
				// Simulate writing a file.
				if err := os.WriteFile(filepath.Join(dir, "feature.go"), []byte("package main\n"), 0644); err != nil {
					return nil, err
				}
				return &agent.Response{Output: "code written"}, nil
			case agent.ModeReview:
				// No findings - clean review.
				return &agent.Response{Output: "[]"}, nil
			}
			return &agent.Response{Output: "ok"}, nil
		},
	}

	mt := newMockTracker()
	mt.Issues["gh:test/repo#10"] = &tracker.Issue{
		ID:      "10",
		Tracker: "github",
		Title:   "Add feature",
		Body:    "Implement the feature",
		Repo:    "test/repo",
	}

	eng, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "github",
		MaxIterations:  3,
	}, map[string]agent.Agent{"mock": ma}, map[string]tracker.Tracker{"github": mt}, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	result, err := eng.Build(context.Background(), BuildRequest{
		IssueRef:   "gh:test/repo#10",
		WorkDir:    dir,
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if result.Status != BuildStatusSuccess {
		t.Errorf("expected status success, got %s (error: %s)", result.Status, result.Error)
	}
	if result.PR == nil {
		t.Error("expected PR to be created")
	}
	if result.Iterations != 1 {
		t.Errorf("expected 1 iteration, got %d", result.Iterations)
	}
	if codeCallCount != 1 {
		t.Errorf("expected 1 code agent call, got %d", codeCallCount)
	}
	if result.Plan == "" {
		t.Error("expected plan to be populated")
	}
	if len(result.Reviews) != 1 {
		t.Errorf("expected 1 review, got %d", len(result.Reviews))
	}
}

func TestBuild_CriticalFindingsThenClean(t *testing.T) {
	dir := initBuildTestRepo(t)

	reviewCallCount := 0
	ma := &mockAgent{
		RunFunc: func(_ context.Context, req agent.Request) (*agent.Response, error) {
			switch req.Mode {
			case agent.ModePlan:
				return &agent.Response{Output: "plan"}, nil
			case agent.ModeCode:
				// Write different content each time.
				if err := os.WriteFile(filepath.Join(dir, "feature.go"), []byte("package main\n// v2\n"), 0644); err != nil {
					return nil, err
				}
				return &agent.Response{Output: "code"}, nil
			case agent.ModeReview:
				reviewCallCount++
				if reviewCallCount == 1 {
					// First review: critical finding.
					findings := []review.Finding{{
						File:        "feature.go",
						Line:        5,
						PrincipleID: "sec-001",
						Severity:    principles.SeverityCritical,
						Message:     "SQL injection",
						Suggestion:  "Use parameterized queries",
					}}
					data, _ := json.Marshal(findings)
					return &agent.Response{Output: string(data)}, nil
				}
				// Second review: clean.
				return &agent.Response{Output: "[]"}, nil
			}
			return &agent.Response{Output: "ok"}, nil
		},
	}

	mt := newMockTracker()
	mt.Issues["gh:test/repo#11"] = &tracker.Issue{
		ID:      "11",
		Tracker: "github",
		Title:   "Fix security",
		Body:    "Fix the security issue",
		Repo:    "test/repo",
	}

	eng, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "github",
		MaxIterations:  3,
	}, map[string]agent.Agent{"mock": ma}, map[string]tracker.Tracker{"github": mt}, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	result, err := eng.Build(context.Background(), BuildRequest{
		IssueRef:   "gh:test/repo#11",
		WorkDir:    dir,
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if result.Status != BuildStatusSuccess {
		t.Errorf("expected success, got %s (error: %s)", result.Status, result.Error)
	}
	if result.Iterations != 2 {
		t.Errorf("expected 2 iterations, got %d", result.Iterations)
	}
	if len(result.Reviews) != 2 {
		t.Errorf("expected 2 reviews, got %d", len(result.Reviews))
	}
	// First review should have had critical findings.
	if !result.Reviews[0].HasCritical {
		t.Error("expected first review to have critical findings")
	}
	// Second review should be clean.
	if result.Reviews[1].HasCritical {
		t.Error("expected second review to be clean")
	}
}

func TestBuild_MaxIterationsReached(t *testing.T) {
	dir := initBuildTestRepo(t)

	ma := &mockAgent{
		RunFunc: func(_ context.Context, req agent.Request) (*agent.Response, error) {
			switch req.Mode {
			case agent.ModePlan:
				return &agent.Response{Output: "plan"}, nil
			case agent.ModeCode:
				if err := os.WriteFile(filepath.Join(dir, "feature.go"), []byte("package main\n"), 0644); err != nil {
					return nil, err
				}
				return &agent.Response{Output: "code"}, nil
			case agent.ModeReview:
				// Always return critical findings.
				findings := []review.Finding{{
					File:        "feature.go",
					Line:        1,
					PrincipleID: "sec-001",
					Severity:    principles.SeverityCritical,
					Message:     "unfixable issue",
				}}
				data, _ := json.Marshal(findings)
				return &agent.Response{Output: string(data)}, nil
			}
			return &agent.Response{Output: "ok"}, nil
		},
	}

	mt := newMockTracker()
	mt.Issues["gh:test/repo#12"] = &tracker.Issue{
		ID:      "12",
		Tracker: "github",
		Title:   "Impossible fix",
		Body:    "This will never pass review",
		Repo:    "test/repo",
	}

	eng, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "github",
		MaxIterations:  2,
	}, map[string]agent.Agent{"mock": ma}, map[string]tracker.Tracker{"github": mt}, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	result, err := eng.Build(context.Background(), BuildRequest{
		IssueRef:   "gh:test/repo#12",
		WorkDir:    dir,
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("Build should not return error for max loops: %v", err)
	}

	if result.Status != BuildStatusMaxLoops {
		t.Errorf("expected max_loops status, got %s", result.Status)
	}
	if result.Iterations != 2 {
		t.Errorf("expected 2 iterations, got %d", result.Iterations)
	}
	if result.PR != nil {
		t.Error("expected no PR when max loops reached")
	}
}

func TestBuild_RequireApproval(t *testing.T) {
	dir := initBuildTestRepo(t)

	ma := &mockAgent{
		RunFunc: func(_ context.Context, req agent.Request) (*agent.Response, error) {
			if req.Mode == agent.ModePlan {
				return &agent.Response{Output: "plan requiring approval"}, nil
			}
			return &agent.Response{Output: "ok"}, nil
		},
	}

	mt := newMockTracker()
	mt.Issues["gh:test/repo#13"] = &tracker.Issue{
		ID:      "13",
		Tracker: "github",
		Title:   "Needs approval",
		Body:    "Must be approved first",
		Repo:    "test/repo",
	}

	eng, err := New(&EngineConfig{
		DefaultAgent:    "mock",
		DefaultTracker:  "github",
		MaxIterations:   3,
		RequireApproval: true,
	}, map[string]agent.Agent{"mock": ma}, map[string]tracker.Tracker{"github": mt}, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	result, err := eng.Build(context.Background(), BuildRequest{
		IssueRef:   "gh:test/repo#13",
		WorkDir:    dir,
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if result.Status != BuildStatusRejected {
		t.Errorf("expected rejected status, got %s", result.Status)
	}
	if result.Plan == "" {
		t.Error("expected plan to be populated")
	}
	if result.PR != nil {
		t.Error("expected no PR when approval required")
	}
}

func TestBuild_InvalidIssueRef(t *testing.T) {
	eng, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "github",
	}, map[string]agent.Agent{"mock": &mockAgent{}}, map[string]tracker.Tracker{"github": newMockTracker()}, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	_, err = eng.Build(context.Background(), BuildRequest{
		IssueRef: "",
		WorkDir:  t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for empty issue ref")
	}
}

func TestBuild_IssueFetchError(t *testing.T) {
	mt := newMockTracker()
	// Don't add any issues, so fetch will fail.

	eng, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "github",
	}, map[string]agent.Agent{"mock": &mockAgent{}}, map[string]tracker.Tracker{"github": mt}, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	_, err = eng.Build(context.Background(), BuildRequest{
		IssueRef: "gh:test/repo#999",
		WorkDir:  t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for missing issue")
	}
}

func TestBuild_FeedbackFromLastReview(t *testing.T) {
	result := &BuildResult{}

	// No reviews yet.
	feedback := result.feedbackFromLastReview()
	if feedback != "" {
		t.Errorf("expected empty feedback with no reviews, got %q", feedback)
	}

	// Add a review with findings.
	result.Reviews = append(result.Reviews, review.Result{
		Findings: []review.Finding{
			{
				File:        "main.go",
				Line:        10,
				Severity:    principles.SeverityCritical,
				Message:     "SQL injection",
				Suggestion:  "Use prepared statements",
				PrincipleID: "sec-001",
			},
			{
				Severity: principles.SeverityWarning,
				Message:  "Missing docs",
			},
		},
		HasCritical: true,
	})

	feedback = result.feedbackFromLastReview()
	if feedback == "" {
		t.Fatal("expected feedback from review with findings")
	}

	// Verify feedback contains relevant information.
	if !contains(feedback, "SQL injection") {
		t.Error("expected feedback to contain finding message")
	}
	if !contains(feedback, "main.go") {
		t.Error("expected feedback to contain file name")
	}
	if !contains(feedback, "Use prepared statements") {
		t.Error("expected feedback to contain suggestion")
	}
}

func TestBuild_CodeAgentReceivesFeedback(t *testing.T) {
	dir := initBuildTestRepo(t)

	reviewCallCount := 0
	var secondCodePrompt string

	ma := &mockAgent{
		RunFunc: func(_ context.Context, req agent.Request) (*agent.Response, error) {
			switch req.Mode {
			case agent.ModePlan:
				return &agent.Response{Output: "plan"}, nil
			case agent.ModeCode:
				if reviewCallCount > 0 {
					secondCodePrompt = req.Prompt
				}
				if err := os.WriteFile(filepath.Join(dir, "feature.go"), []byte("package main\n"), 0644); err != nil {
					return nil, err
				}
				return &agent.Response{Output: "code"}, nil
			case agent.ModeReview:
				reviewCallCount++
				if reviewCallCount == 1 {
					findings := []review.Finding{{
						File:     "feature.go",
						Line:     1,
						Severity: principles.SeverityCritical,
						Message:  "needs error handling",
					}}
					data, _ := json.Marshal(findings)
					return &agent.Response{Output: string(data)}, nil
				}
				return &agent.Response{Output: "[]"}, nil
			}
			return &agent.Response{Output: "ok"}, nil
		},
	}

	mt := newMockTracker()
	mt.Issues["gh:test/repo#14"] = &tracker.Issue{
		ID:      "14",
		Tracker: "github",
		Title:   "Feedback test",
		Body:    "Test feedback propagation",
		Repo:    "test/repo",
	}

	eng, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "github",
		MaxIterations:  3,
	}, map[string]agent.Agent{"mock": ma}, map[string]tracker.Tracker{"github": mt}, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	result, err := eng.Build(context.Background(), BuildRequest{
		IssueRef:   "gh:test/repo#14",
		WorkDir:    dir,
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if result.Status != BuildStatusSuccess {
		t.Errorf("expected success, got %s", result.Status)
	}

	// Verify the second code call received feedback.
	if !contains(secondCodePrompt, "needs error handling") {
		t.Error("expected second code prompt to contain review feedback")
	}
	if !contains(secondCodePrompt, "Review Feedback") {
		t.Error("expected second code prompt to contain 'Review Feedback' section")
	}
}

func TestCountCritical(t *testing.T) {
	tests := []struct {
		name     string
		findings []review.Finding
		want     int
	}{
		{
			name:     "empty findings",
			findings: nil,
			want:     0,
		},
		{
			name: "no critical findings",
			findings: []review.Finding{
				{Severity: principles.SeverityWarning, Message: "warning"},
				{Severity: principles.SeverityInfo, Message: "info"},
			},
			want: 0,
		},
		{
			name: "one critical finding",
			findings: []review.Finding{
				{Severity: principles.SeverityCritical, Message: "critical"},
				{Severity: principles.SeverityWarning, Message: "warning"},
			},
			want: 1,
		},
		{
			name: "multiple critical findings",
			findings: []review.Finding{
				{Severity: principles.SeverityCritical, Message: "crit 1"},
				{Severity: principles.SeverityCritical, Message: "crit 2"},
				{Severity: principles.SeverityInfo, Message: "info"},
			},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countCritical(tt.findings)
			if got != tt.want {
				t.Errorf("countCritical() = %d, want %d", got, tt.want)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && containsStr(s, substr)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
