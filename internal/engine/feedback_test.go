package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/jelmersnoeck/forge/internal/agent"
)

func TestFeedback_Success(t *testing.T) {
	ma := &mockAgent{
		RunFunc: func(_ context.Context, req agent.Request) (*agent.Response, error) {
			return &agent.Response{
				Output: "applied feedback changes",
				Files:  []string{"main.go", "handler.go"},
			}, nil
		},
	}

	eng, err := New(&EngineConfig{
		DefaultAgent: "mock",
	}, map[string]agent.Agent{"mock": ma}, nil, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	result, err := eng.Feedback(context.Background(), FeedbackRequest{
		PRNumber:     42,
		RepoFullName: "org/repo",
		ReviewBody:   "Please fix the error handling",
		Comments: []ReviewComment{
			{
				Path: "main.go",
				Line: 10,
				Body: "This should return an error, not panic",
			},
		},
		WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Feedback: %v", err)
	}

	if result.Status != "applied" {
		t.Errorf("expected status 'applied', got %q", result.Status)
	}
	if len(result.FilesChanged) != 2 {
		t.Errorf("expected 2 files changed, got %d", len(result.FilesChanged))
	}
}

func TestFeedback_AgentError(t *testing.T) {
	ma := &mockAgent{
		RunFunc: func(_ context.Context, req agent.Request) (*agent.Response, error) {
			return &agent.Response{
				Error: "agent crashed",
			}, nil
		},
	}

	eng, err := New(&EngineConfig{
		DefaultAgent: "mock",
	}, map[string]agent.Agent{"mock": ma}, nil, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	result, err := eng.Feedback(context.Background(), FeedbackRequest{
		PRNumber:     42,
		RepoFullName: "org/repo",
		ReviewBody:   "Fix it",
		WorkDir:      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Feedback should not return error for agent error: %v", err)
	}

	if result.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", result.Status)
	}
}

func TestFeedback_AgentRunError(t *testing.T) {
	ma := &mockAgent{
		RunFunc: func(_ context.Context, req agent.Request) (*agent.Response, error) {
			return nil, context.Canceled
		},
	}

	eng, err := New(&EngineConfig{
		DefaultAgent: "mock",
	}, map[string]agent.Agent{"mock": ma}, nil, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	_, err = eng.Feedback(context.Background(), FeedbackRequest{
		PRNumber:     42,
		RepoFullName: "org/repo",
		ReviewBody:   "Fix it",
		WorkDir:      t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error when agent.Run returns error")
	}
}

func TestFeedback_EmptyComments(t *testing.T) {
	ma := &mockAgent{
		RunFunc: func(_ context.Context, req agent.Request) (*agent.Response, error) {
			return &agent.Response{
				Output: "no inline comments to process",
			}, nil
		},
	}

	eng, err := New(&EngineConfig{
		DefaultAgent: "mock",
	}, map[string]agent.Agent{"mock": ma}, nil, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	result, err := eng.Feedback(context.Background(), FeedbackRequest{
		PRNumber:     42,
		RepoFullName: "org/repo",
		ReviewBody:   "Looks good overall but needs polish",
		WorkDir:      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Feedback: %v", err)
	}

	if result.Status != "applied" {
		t.Errorf("expected status 'applied', got %q", result.Status)
	}
}

func TestFeedback_MissingCoderAgent(t *testing.T) {
	eng, err := New(&EngineConfig{
		DefaultAgent: "mock",
		CoderAgent:   "nonexistent",
	}, map[string]agent.Agent{"mock": &mockAgent{}}, nil, nil)

	// Engine creation should fail with nonexistent agent.
	if err == nil {
		// If it somehow passes, feedback should fail.
		_, err = eng.Feedback(context.Background(), FeedbackRequest{
			PRNumber:     1,
			RepoFullName: "org/repo",
			WorkDir:      t.TempDir(),
		})
		if err == nil {
			t.Fatal("expected error for missing coder agent")
		}
	}
}

func TestFeedback_PromptContainsFileAndLineContext(t *testing.T) {
	var capturedPrompt string
	ma := &mockAgent{
		RunFunc: func(_ context.Context, req agent.Request) (*agent.Response, error) {
			capturedPrompt = req.Prompt
			return &agent.Response{Output: "ok"}, nil
		},
	}

	eng, err := New(&EngineConfig{
		DefaultAgent: "mock",
	}, map[string]agent.Agent{"mock": ma}, nil, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	_, err = eng.Feedback(context.Background(), FeedbackRequest{
		PRNumber:     99,
		RepoFullName: "acme/app",
		ReviewBody:   "Major issues found",
		Comments: []ReviewComment{
			{
				Path:     "src/handler.go",
				Line:     42,
				Body:     "Missing nil check here",
				DiffHunk: "@@ -40,6 +40,8 @@ func Handle()",
			},
			{
				Path: "src/model.go",
				Body: "Add validation",
			},
		},
		WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Feedback: %v", err)
	}

	// Verify the prompt contains expected content.
	checks := []struct {
		label string
		want  string
	}{
		{"PR number", "PR #99"},
		{"repo name", "acme/app"},
		{"review body", "Major issues found"},
		{"file path", "src/handler.go"},
		{"line number", "42"},
		{"comment body", "Missing nil check here"},
		{"diff hunk", "@@ -40,6 +40,8 @@ func Handle()"},
		{"second file", "src/model.go"},
		{"second comment", "Add validation"},
		{"authority instruction", "Human reviewers have highest authority"},
	}

	for _, c := range checks {
		if !strings.Contains(capturedPrompt, c.want) {
			t.Errorf("prompt missing %s (%q)", c.label, c.want)
		}
	}
}

func TestFeedback_AgentCalledWithCodeMode(t *testing.T) {
	ma := &mockAgent{
		RunFunc: func(_ context.Context, req agent.Request) (*agent.Response, error) {
			if req.Mode != agent.ModeCode {
				t.Errorf("expected ModeCode, got %q", req.Mode)
			}
			if !req.Permissions.Write {
				t.Error("expected write permission to be true")
			}
			if !req.Permissions.Execute {
				t.Error("expected execute permission to be true")
			}
			return &agent.Response{Output: "ok"}, nil
		},
	}

	eng, err := New(&EngineConfig{
		DefaultAgent: "mock",
	}, map[string]agent.Agent{"mock": ma}, nil, nil)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	_, err = eng.Feedback(context.Background(), FeedbackRequest{
		PRNumber:     1,
		RepoFullName: "org/repo",
		ReviewBody:   "Fix",
		WorkDir:      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Feedback: %v", err)
	}
}

func TestBuildFeedbackPrompt(t *testing.T) {
	tests := []struct {
		name     string
		req      FeedbackRequest
		contains []string
	}{
		{
			name: "full request",
			req: FeedbackRequest{
				PRNumber:     10,
				RepoFullName: "org/repo",
				ReviewBody:   "Please fix",
				Comments: []ReviewComment{
					{
						Path:     "main.go",
						Line:     5,
						Body:     "Error here",
						DiffHunk: "@@ -1,3 +1,5 @@",
					},
				},
			},
			contains: []string{
				"PR #10",
				"org/repo",
				"Please fix",
				"main.go",
				"5",
				"Error here",
				"@@ -1,3 +1,5 @@",
				"Human reviewers have highest authority",
			},
		},
		{
			name: "no review body",
			req: FeedbackRequest{
				PRNumber:     7,
				RepoFullName: "org/repo",
				Comments: []ReviewComment{
					{Path: "file.go", Body: "Fix this"},
				},
			},
			contains: []string{
				"PR #7",
				"Fix this",
			},
		},
		{
			name: "no comments",
			req: FeedbackRequest{
				PRNumber:     3,
				RepoFullName: "org/repo",
				ReviewBody:   "Overall feedback only",
			},
			contains: []string{
				"PR #3",
				"Overall feedback only",
			},
		},
		{
			name: "comment without line number",
			req: FeedbackRequest{
				PRNumber:     1,
				RepoFullName: "org/repo",
				Comments: []ReviewComment{
					{Path: "readme.md", Body: "Update docs"},
				},
			},
			contains: []string{
				"readme.md",
				"Update docs",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := buildFeedbackPrompt(tt.req)
			for _, want := range tt.contains {
				if !strings.Contains(prompt, want) {
					t.Errorf("prompt missing %q", want)
				}
			}
		})
	}
}
