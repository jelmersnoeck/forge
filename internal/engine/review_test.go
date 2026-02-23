package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jelmersnoeck/forge/internal/agent"
	"github.com/jelmersnoeck/forge/internal/principles"
	"github.com/jelmersnoeck/forge/internal/review"
	"github.com/jelmersnoeck/forge/internal/tracker"
)

func TestReview_EmptyDiff(t *testing.T) {
	eng, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "github",
	}, map[string]agent.Agent{"mock": &mockAgent{}}, map[string]tracker.Tracker{"github": newMockTracker()}, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	result, err := eng.Review(context.Background(), ReviewRequest{
		Diff: "",
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if result.HasCritical {
		t.Error("expected no critical findings for empty diff")
	}
	if len(result.Findings) != 0 {
		t.Errorf("expected no findings, got %d", len(result.Findings))
	}
}

func TestReview_NoFindings(t *testing.T) {
	ma := &mockAgent{
		RunFunc: func(_ context.Context, req agent.Request) (*agent.Response, error) {
			if req.Mode != agent.ModeReview {
				t.Errorf("expected review mode, got %s", req.Mode)
			}
			return &agent.Response{Output: "[]"}, nil
		},
	}

	eng, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "github",
	}, map[string]agent.Agent{"mock": ma}, map[string]tracker.Tracker{"github": newMockTracker()}, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	result, err := eng.Review(context.Background(), ReviewRequest{
		Diff:    "diff --git a/file.go b/file.go\n+hello\n",
		WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if result.HasCritical {
		t.Error("expected no critical findings")
	}
}

func TestReview_WithCriticalFindings(t *testing.T) {
	findings := []review.Finding{
		{
			File:        "main.go",
			Line:        10,
			PrincipleID: "sec-001",
			Severity:    principles.SeverityCritical,
			Message:     "SQL injection vulnerability",
			Suggestion:  "Use parameterized queries",
			Reviewer:    "mock",
		},
	}
	findingsJSON, _ := json.Marshal(findings)

	ma := &mockAgent{
		RunFunc: func(_ context.Context, _ agent.Request) (*agent.Response, error) {
			return &agent.Response{Output: string(findingsJSON)}, nil
		},
	}

	eng, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "github",
	}, map[string]agent.Agent{"mock": ma}, map[string]tracker.Tracker{"github": newMockTracker()}, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	result, err := eng.Review(context.Background(), ReviewRequest{
		Diff:    "diff --git a/main.go b/main.go\n+db.Query(userInput)\n",
		WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !result.HasCritical {
		t.Error("expected critical findings")
	}
	if len(result.Findings) != 1 {
		t.Errorf("expected 1 finding, got %d", len(result.Findings))
	}
	if result.Findings[0].PrincipleID != "sec-001" {
		t.Errorf("expected principle sec-001, got %s", result.Findings[0].PrincipleID)
	}
}

func TestReview_WithWrappedFindings(t *testing.T) {
	// Test parsing of {"findings": [...]} format.
	findings := struct {
		Findings []review.Finding `json:"findings"`
	}{
		Findings: []review.Finding{
			{
				File:        "api.go",
				Line:        25,
				PrincipleID: "arch-001",
				Severity:    principles.SeverityWarning,
				Message:     "Missing error handling",
			},
		},
	}
	findingsJSON, _ := json.Marshal(findings)

	ma := &mockAgent{
		RunFunc: func(_ context.Context, _ agent.Request) (*agent.Response, error) {
			return &agent.Response{Output: string(findingsJSON)}, nil
		},
	}

	eng, err := New(&EngineConfig{
		DefaultAgent:      "mock",
		DefaultTracker:    "github",
		SeverityThreshold: "warning",
	}, map[string]agent.Agent{"mock": ma}, map[string]tracker.Tracker{"github": newMockTracker()}, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	result, err := eng.Review(context.Background(), ReviewRequest{
		Diff:    "diff --git a/api.go b/api.go\n+result, _ := doSomething()\n",
		WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if result.HasCritical {
		t.Error("expected no critical findings")
	}
	if len(result.Findings) != 1 {
		t.Errorf("expected 1 finding, got %d", len(result.Findings))
	}
}

func TestReview_UnparsableOutput(t *testing.T) {
	ma := &mockAgent{
		RunFunc: func(_ context.Context, _ agent.Request) (*agent.Response, error) {
			return &agent.Response{Output: "this is not JSON at all"}, nil
		},
	}

	eng, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "github",
	}, map[string]agent.Agent{"mock": ma}, map[string]tracker.Tracker{"github": newMockTracker()}, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	// Unparsable output should return an error, not a clean pass.
	_, err = eng.Review(context.Background(), ReviewRequest{
		Diff:    "some diff",
		WorkDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for unparsable review output")
	}
	if !strings.Contains(err.Error(), "parsing review findings") {
		t.Errorf("expected parse error, got: %v", err)
	}
}

func TestReview_PromptContainsDiff(t *testing.T) {
	var capturedPrompt string
	ma := &mockAgent{
		RunFunc: func(_ context.Context, req agent.Request) (*agent.Response, error) {
			capturedPrompt = req.Prompt
			return &agent.Response{Output: "[]"}, nil
		},
	}

	eng, err := New(&EngineConfig{
		DefaultAgent:   "mock",
		DefaultTracker: "github",
	}, map[string]agent.Agent{"mock": ma}, map[string]tracker.Tracker{"github": newMockTracker()}, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	testDiff := "diff --git a/foo.go b/foo.go\n+new line\n"
	_, err = eng.Review(context.Background(), ReviewRequest{
		Diff:    testDiff,
		WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	if !strings.Contains(capturedPrompt, testDiff) {
		t.Error("expected review prompt to contain the diff")
	}
}

func TestReview_SeverityThresholdFiltering(t *testing.T) {
	findings := []review.Finding{
		{File: "a.go", Line: 1, PrincipleID: "info-1", Severity: principles.SeverityInfo, Message: "info"},
		{File: "b.go", Line: 2, PrincipleID: "warn-1", Severity: principles.SeverityWarning, Message: "warning"},
		{File: "c.go", Line: 3, PrincipleID: "crit-1", Severity: principles.SeverityCritical, Message: "critical"},
	}
	findingsJSON, _ := json.Marshal(findings)

	ma := &mockAgent{
		RunFunc: func(_ context.Context, _ agent.Request) (*agent.Response, error) {
			return &agent.Response{Output: string(findingsJSON)}, nil
		},
	}

	tests := []struct {
		name      string
		threshold string
		wantCount int
	}{
		{"critical threshold", "critical", 1},
		{"warning threshold", "warning", 2},
		{"info threshold", "info", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng, err := New(&EngineConfig{
				DefaultAgent:      "mock",
				DefaultTracker:    "github",
				SeverityThreshold: tt.threshold,
			}, map[string]agent.Agent{"mock": ma}, map[string]tracker.Tracker{"github": newMockTracker()}, nil)
			if err != nil {
				t.Fatalf("New engine: %v", err)
			}

			result, err := eng.Review(context.Background(), ReviewRequest{
				Diff:    "some diff",
				WorkDir: t.TempDir(),
			})
			if err != nil {
				t.Fatalf("Review: %v", err)
			}
			if len(result.Findings) != tt.wantCount {
				t.Errorf("expected %d findings with %s threshold, got %d", tt.wantCount, tt.threshold, len(result.Findings))
			}
		})
	}
}

func TestReviewParseFindingsIntegration(t *testing.T) {
	// Tests that review.ParseFindings is correctly used by the engine.
	// Detailed parsing tests live in the review package.
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{
			name:  "empty string",
			input: "",
			want:  0,
		},
		{
			name:  "empty array",
			input: "[]",
			want:  0,
		},
		{
			name:  "single finding as array",
			input: `[{"file":"a.go","line":1,"principle_id":"sec-001","severity":"critical","message":"bad"}]`,
			want:  1,
		},
		{
			name:  "findings embedded in wrapper object",
			input: `{"findings":[{"file":"a.go","line":1,"principle_id":"sec-001","severity":"critical","message":"bad"}]}`,
			want:  1,
		},
		{
			name:    "invalid JSON",
			input:   "not json",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := review.ParseFindings(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("review.ParseFindings() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if len(got) != tt.want {
				t.Errorf("review.ParseFindings() got %d findings, want %d", len(got), tt.want)
			}
		})
	}
}

func TestParseSeverity(t *testing.T) {
	tests := []struct {
		input string
		want  principles.Severity
	}{
		{"info", principles.SeverityInfo},
		{"warning", principles.SeverityWarning},
		{"critical", principles.SeverityCritical},
		{"unknown", principles.SeverityCritical},
		{"", principles.SeverityCritical},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseSeverity(tt.input)
			if got != tt.want {
				t.Errorf("parseSeverity(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
