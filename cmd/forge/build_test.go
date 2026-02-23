package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jelmersnoeck/forge/internal/engine"
	"github.com/jelmersnoeck/forge/internal/review"
	"github.com/jelmersnoeck/forge/internal/tracker"
	"github.com/jelmersnoeck/forge/pkg/config"
)

func TestSplitAndTrim(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "single value",
			input: "security",
			want:  []string{"security"},
		},
		{
			name:  "multiple values",
			input: "security,architecture,testing",
			want:  []string{"security", "architecture", "testing"},
		},
		{
			name:  "with whitespace",
			input: " security , architecture , testing ",
			want:  []string{"security", "architecture", "testing"},
		},
		{
			name:  "empty entries filtered",
			input: "security,,testing,",
			want:  []string{"security", "testing"},
		},
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitAndTrim(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("splitAndTrim(%q) = %v, want %v", tt.input, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitAndTrim(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestOutputJSON(t *testing.T) {
	result := &engine.BuildResult{
		Status: engine.BuildStatusSuccess,
		Plan:   "test plan",
		Issue: &tracker.Issue{
			ID:    "42",
			Title: "Test Issue",
		},
	}

	// outputJSON writes to stdout; we just verify it does not error.
	err := outputJSON(result)
	if err != nil {
		t.Fatalf("outputJSON() error: %v", err)
	}
}

func TestOutputBuildText_Success(t *testing.T) {
	result := &engine.BuildResult{
		Status: engine.BuildStatusSuccess,
		Issue: &tracker.Issue{
			ID:    "42",
			Title: "Test Issue",
		},
		Plan:       "Step 1: Do something",
		Iterations: 1,
		PR: &tracker.PullRequest{
			URL: "https://github.com/org/repo/pull/99",
		},
	}

	// Verify no error.
	err := outputBuildText(result)
	if err != nil {
		t.Fatalf("outputBuildText() error: %v", err)
	}
}

func TestOutputBuildText_WithFindings(t *testing.T) {
	result := &engine.BuildResult{
		Status:     engine.BuildStatusMaxLoops,
		Iterations: 3,
		Issue: &tracker.Issue{
			ID:    "42",
			Title: "Test Issue",
		},
		Reviews: []review.Result{
			{
				Findings: []review.Finding{
					{
						Severity: "critical",
						Message:  "SQL injection vulnerability",
						File:     "db.go",
						Line:     42,
					},
				},
				HasCritical: true,
			},
		},
		Error: "max iterations (3) reached with critical findings",
	}

	err := outputBuildText(result)
	if err != nil {
		t.Fatalf("outputBuildText() error: %v", err)
	}
}

func TestBuildCmdFlags(t *testing.T) {
	// Verify the build command has the expected flags.
	flags := buildCmd.Flags()

	issueFlag := flags.Lookup("issue")
	if issueFlag == nil {
		t.Fatal("build command missing --issue flag")
	}

	principlesFlag := flags.Lookup("principles")
	if principlesFlag == nil {
		t.Fatal("build command missing --principles flag")
	}

	branchFlag := flags.Lookup("branch")
	if branchFlag == nil {
		t.Fatal("build command missing --branch flag")
	}
	if branchFlag.DefValue != "main" {
		t.Errorf("--branch default = %q, want %q", branchFlag.DefValue, "main")
	}

	formatFlag := flags.Lookup("format")
	if formatFlag == nil {
		t.Fatal("build command missing --format flag")
	}
	if formatFlag.DefValue != "text" {
		t.Errorf("--format default = %q, want %q", formatFlag.DefValue, "text")
	}

	workstreamFlag := flags.Lookup("workstream")
	if workstreamFlag == nil {
		t.Fatal("build command missing --workstream flag")
	}
}

func TestBuildResultJSON(t *testing.T) {
	result := &engine.BuildResult{
		Status:     engine.BuildStatusSuccess,
		Plan:       "test plan",
		Iterations: 1,
		Issue: &tracker.Issue{
			ID:      "42",
			Tracker: "github",
			Title:   "Test Issue",
		},
		PR: &tracker.PullRequest{
			URL: "https://github.com/org/repo/pull/99",
		},
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("JSON marshal error: %v", err)
	}

	jsonStr := string(data)

	// BuildResult fields use Go struct field names since they lack json tags.
	if !strings.Contains(jsonStr, `"Status": "success"`) {
		t.Errorf("JSON output missing Status field, got:\n%s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"Plan": "test plan"`) {
		t.Errorf("JSON output missing Plan field, got:\n%s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"URL": "https://github.com/org/repo/pull/99"`) {
		t.Errorf("JSON output missing PR URL, got:\n%s", jsonStr)
	}
}

func TestBuildEngine_DefaultConfig(t *testing.T) {
	cfg := config.Default()

	eng, err := buildEngine(cfg)
	if err != nil {
		t.Fatalf("buildEngine() error: %v", err)
	}

	if eng == nil {
		t.Fatal("buildEngine() returned nil engine")
	}

	if eng.Config.MaxIterations != 3 {
		t.Errorf("MaxIterations = %d, want 3", eng.Config.MaxIterations)
	}

	if eng.Config.DefaultAgent != "claude-code" {
		t.Errorf("DefaultAgent = %q, want %q", eng.Config.DefaultAgent, "claude-code")
	}

	if eng.Config.DefaultTracker != "github" {
		t.Errorf("DefaultTracker = %q, want %q", eng.Config.DefaultTracker, "github")
	}
}

func TestBuildEngine_FileTracker(t *testing.T) {
	cfg := config.Default()
	cfg.Tracker.Default = "file"

	eng, err := buildEngine(cfg)
	if err != nil {
		t.Fatalf("buildEngine() error: %v", err)
	}

	if _, ok := eng.Trackers["file"]; !ok {
		t.Error("expected file tracker to be registered")
	}
}

func TestBuildEngine_NoBackends(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.Backends = nil

	_, err := buildEngine(cfg)
	if err == nil {
		t.Fatal("expected error when no agent backends configured")
	}
	if !strings.Contains(err.Error(), "no agent backends") {
		t.Errorf("error = %q, want it to mention 'no agent backends'", err.Error())
	}
}

func TestBuildEngine_CustomRoles(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.Backends["opencode"] = config.AgentBackend{Binary: "opencode"}
	cfg.Agent.Roles.Reviewer = "opencode"

	eng, err := buildEngine(cfg)
	if err != nil {
		t.Fatalf("buildEngine() error: %v", err)
	}

	if eng.Config.ReviewerAgent != "opencode" {
		t.Errorf("ReviewerAgent = %q, want %q", eng.Config.ReviewerAgent, "opencode")
	}

	if _, ok := eng.Agents["opencode"]; !ok {
		t.Error("expected opencode agent to be registered")
	}
}
