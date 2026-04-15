package phase

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jelmersnoeck/forge/internal/review"
	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestNewSWEOrchestrator(t *testing.T) {
	r := require.New(t)
	orch := NewSWEOrchestrator()
	r.NotNil(orch)
	r.Equal(maxReviewCycles, orch.maxReviewCycles)
}

func TestBuildCoderPrompt(t *testing.T) {
	tests := map[string]struct {
		specPath     string
		wantContains string
	}{
		"no spec": {
			specPath:     "",
			wantContains: "Implement the changes",
		},
		"spec path that doesn't exist": {
			specPath:     "/nonexistent/greendale/paintball-spec.md",
			wantContains: "Read the spec file first",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			prompt := buildCoderPrompt(tc.specPath)
			r.Contains(prompt, tc.wantContains)
		})
	}
}

func TestBuildCoderPromptWithRealFile(t *testing.T) {
	r := require.New(t)

	// Write a spec file.
	dir := t.TempDir()
	specPath := filepath.Join(dir, "test-spec.md")
	content := "# Troy Barnes enrollment spec\n\nHe's a football star at Greendale."
	r.NoError(os.WriteFile(specPath, []byte(content), 0o644))

	prompt := buildCoderPrompt(specPath)
	r.Contains(prompt, "Troy Barnes enrollment spec")
	r.Contains(prompt, "football star at Greendale")
	r.Contains(prompt, "source of truth")
}

func TestFormatFindingsForCoder(t *testing.T) {
	r := require.New(t)

	findings := []review.Finding{
		{
			Severity:    review.SeverityCritical,
			File:        "internal/paintball/gun.go",
			StartLine:   42,
			Description: "SQL injection in query builder",
		},
		{
			Severity:    review.SeverityPraise,
			Description: "Great error handling in Human Being mascot module",
		},
		{
			Severity:    review.SeverityWarning,
			Description: "Missing nil check on Señor Chang's credentials",
		},
	}

	result := formatFindingsForCoder(findings)
	r.Contains(result, "SQL injection")
	r.Contains(result, "internal/paintball/gun.go:42")
	r.Contains(result, "Missing nil check")
	// Praise should be excluded.
	r.NotContains(result, "Great error handling")
}

func TestFormatFindingsForCoderEmpty(t *testing.T) {
	r := require.New(t)
	result := formatFindingsForCoder(nil)
	r.Contains(result, "code review found the following issues")
}

func TestEmitHelpers(t *testing.T) {
	r := require.New(t)
	orch := NewSWEOrchestrator()

	var events []string
	opts := OrchestratorOpts{
		SessionID: "greendale-101",
		Emit: func(e types.OutboundEvent) {
			events = append(events, e.Type+":"+e.Content)
		},
	}

	orch.emitPhaseStart(opts, "spec")
	orch.emitPhaseComplete(opts, "spec", "done")
	orch.emitPhaseHandoff(opts, "spec", "code")

	r.Len(events, 3)
	r.Equal("phase_start:spec", events[0])
	r.Equal("phase_complete:spec: done", events[1])
	r.Contains(events[2], "phase_handoff:")
	r.Contains(events[2], "spec → code")
}

func TestOrchestratorResult(t *testing.T) {
	r := require.New(t)

	// OrchestratorResult with question intent.
	result := OrchestratorResult{
		Intent:      IntentQuestion,
		QAHistoryID: "greendale-history-101",
	}
	r.Equal(IntentQuestion, result.Intent)
	r.Equal("greendale-history-101", result.QAHistoryID)

	// OrchestratorResult with task intent — QAHistoryID should be empty.
	taskResult := OrchestratorResult{
		Intent: IntentTask,
	}
	r.Equal(IntentTask, taskResult.Intent)
	r.Empty(taskResult.QAHistoryID)
}

func TestQAPhaseDisallowedTools(t *testing.T) {
	r := require.New(t)
	qa := QA()

	// Verify all spec-mandated disallowed tools are present.
	wantDisallowed := []string{
		"Write", "Edit",
		"Agent", "AgentGet", "AgentList", "AgentStop",
		"TaskCreate", "TaskGet", "TaskList", "TaskStop", "TaskOutput",
		"QueueImmediate", "QueueOnComplete",
		"UseMCPTool",
		"Reflect",
	}

	for _, tool := range wantDisallowed {
		r.Contains(qa.DisallowedTools, tool,
			"QA phase should disallow %s", tool)
	}

	// Read-only tools should NOT be disallowed.
	readOnlyTools := []string{"Read", "Grep", "Glob", "Bash", "WebSearch"}
	for _, tool := range readOnlyTools {
		r.NotContains(qa.DisallowedTools, tool,
			"QA phase should allow %s", tool)
	}
}

func TestShouldCreatePR(t *testing.T) {
	tests := map[string]struct {
		setup      func(t *testing.T) string
		want       bool
		wantReason string
	}{
		"not a git repo": {
			setup: func(t *testing.T) string {
				return t.TempDir()
			},
			want:       false,
			wantReason: "not a git repository",
		},
		"on main branch": {
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				run(t, dir, "git", "init", "-b", "main")
				run(t, dir, "git", "commit", "--allow-empty", "-m", "init")
				return dir
			},
			want:       false,
			wantReason: "on main branch",
		},
		"on master branch": {
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				run(t, dir, "git", "init", "-b", "master")
				run(t, dir, "git", "commit", "--allow-empty", "-m", "init")
				return dir
			},
			want:       false,
			wantReason: "on master branch",
		},
		"feature branch with changes": {
			setup: func(t *testing.T) string {
				remote, local := initGitRepoWithRemote(t, "main")
				_ = remote
				run(t, local, "git", "checkout", "-b", "jelmer/cool-feature")
				writeTestFile(t, local, "new.txt", "Greendale rules")
				run(t, local, "git", "add", ".")
				run(t, local, "git", "commit", "-m", "add new file")
				return local
			},
			want: true,
		},
		"feature branch no changes": {
			setup: func(t *testing.T) string {
				remote, local := initGitRepoWithRemote(t, "main")
				_ = remote
				run(t, local, "git", "checkout", "-b", "jelmer/empty-feature")
				return local
			},
			want:       false,
			wantReason: "no changes relative to base",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			dir := tc.setup(t)
			got, reason := shouldCreatePR(dir)
			r.Equal(tc.want, got)
			if tc.wantReason != "" {
				r.Contains(reason, tc.wantReason)
			}
		})
	}
}

func TestBranchToTitle(t *testing.T) {
	tests := map[string]struct {
		branch string
		want   string
	}{
		"feature branch": {
			branch: "jelmer/add-paintball-tournament",
			want:   "Add paintball tournament",
		},
		"simple branch": {
			branch: "fix-scoring",
			want:   "Fix scoring",
		},
		"underscores": {
			branch: "jelmer/add_human_being_mascot",
			want:   "Add human being mascot",
		},
		"empty after strip": {
			branch: "jelmer/",
			want:   "Update implementation",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got := branchToTitle(tc.branch)
			r.Equal(tc.want, got)
		})
	}
}

func TestFallbackPRContent(t *testing.T) {
	r := require.New(t)

	title, body := fallbackPRContent(
		"jelmer/add-paintball",
		"abc123 add tournament scaffold\ndef456 add scoring",
		" 2 files changed, 42 insertions(+)",
		"# Add paintball tournament\n\n## Description\nGreendale needs this.",
	)

	r.Equal("Add paintball", title)
	r.Contains(body, "Add paintball tournament")
	r.Contains(body, "2 files changed")
	r.Contains(body, "add tournament scaffold")
}

func TestParsePRContent(t *testing.T) {
	tests := map[string]struct {
		raw       string
		wantTitle string
		wantBody  string
		wantErr   bool
	}{
		"valid json": {
			raw:       `{"title": "Add paintball scoring", "body": "This PR adds scoring for Greendale's annual paintball tournament."}`,
			wantTitle: "Add paintball scoring",
			wantBody:  "This PR adds scoring for Greendale's annual paintball tournament.",
		},
		"json in code fence": {
			raw:       "```json\n{\"title\": \"Fix scoring\", \"body\": \"Fixes a bug in the scoring module.\"}\n```",
			wantTitle: "Fix scoring",
			wantBody:  "Fixes a bug in the scoring module.",
		},
		"empty title": {
			raw:     `{"title": "", "body": "Something"}`,
			wantErr: true,
		},
		"invalid json": {
			raw:     "not json at all",
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			title, body, err := parsePRContent(tc.raw)
			if tc.wantErr {
				r.Error(err)
				return
			}
			r.NoError(err)
			r.Equal(tc.wantTitle, title)
			r.Equal(tc.wantBody, body)
		})
	}
}

func TestValidatePRTitle(t *testing.T) {
	tests := map[string]struct {
		title   string
		wantErr bool
	}{
		"empty":              {title: "", wantErr: true},
		"too short":          {title: "fix auth", wantErr: true},
		"generic":            {title: "fix bug", wantErr: true},
		"good":               {title: "Add OAuth2 authentication for Greendale API", wantErr: false},
		"minimum length":     {title: "Fix user signup", wantErr: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			err := validatePRTitle(tc.title)
			if tc.wantErr {
				r.Error(err)
				return
			}
			r.NoError(err)
		})
	}
}

func TestValidatePRDescription(t *testing.T) {
	tests := map[string]struct {
		body    string
		wantErr bool
	}{
		"empty":           {body: "", wantErr: true},
		"too short":       {body: "Fixed the thing.", wantErr: true},
		"good enough":     {body: "This PR adds the Greendale Human Being mascot to the interactive campus map, including SVG assets.", wantErr: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			err := validatePRDescription(tc.body)
			if tc.wantErr {
				r.Error(err)
				return
			}
			r.NoError(err)
		})
	}
}

// initGitRepoWithRemote creates a bare remote and a local clone on the given branch.
func initGitRepoWithRemote(t *testing.T, branch string) (remote, local string) {
	t.Helper()

	remote = filepath.Join(t.TempDir(), "remote.git")
	run(t, "", "git", "init", "--bare", "-b", branch, remote)

	local = filepath.Join(t.TempDir(), "local")
	run(t, "", "git", "clone", remote, local)
	run(t, local, "git", "checkout", "-b", branch)

	writeTestFile(t, local, "README.md", "# Greendale Community College")
	run(t, local, "git", "add", ".")
	run(t, local, "git", "commit", "-m", "Initial commit: Troy and Abed in the morning")
	run(t, local, "git", "push", "origin", branch)

	return remote, local
}

// run executes a command in the given directory. Fails the test on error.
func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}

// writeTestFile creates a file with the given content.
func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}
